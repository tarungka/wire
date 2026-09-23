package worker_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

type lateRecoverySource struct {
	checkpointTestSource
	phase        *atomic.Int32
	fail         *atomic.Bool
	restored     *atomic.Bool
	needsRestore bool
	offset       byte
}

func (s *lateRecoverySource) Checkpoint(uint64) ([]byte, error) { return []byte{s.offset}, nil }
func (s *lateRecoverySource) RestoreCheckpoint(data []byte) error {
	if len(data) != 1 || data[0] != 1 {
		return fmt.Errorf("unexpected source checkpoint %v", data)
	}
	s.offset = data[0]
	s.restored.Store(true)
	return nil
}
func (s *lateRecoverySource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	if s.needsRestore && !s.restored.Load() {
		return nil, fmt.Errorf("read before restore")
	}
	if s.fail.Swap(false) {
		return nil, fmt.Errorf("injected failure after late output")
	}
	if int32(s.offset) < s.phase.Load() {
		s.offset++
		switch s.offset {
		case 1:
			return []engine.Event{{Key: []byte("k"), Value: []byte("first"), EventTime: 1}, {Key: []byte("clock"), EventTime: 11}}, nil
		case 2:
			return []engine.Event{{Key: []byte("k"), Value: []byte("update"), EventTime: 1}}, nil
		case 3:
			return []engine.Event{{Key: []byte("clock"), EventTime: 100}}, nil
		case 4:
			return []engine.Event{{Key: []byte("k"), Value: []byte("expired"), EventTime: 1, Headers: map[string][]byte{"original": []byte("yes")}}}, nil
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return []engine.Event{}, nil
	}
}

type lateRecoveryObservations struct {
	mu         sync.Mutex
	main, late []engine.Event
}

func (o *lateRecoveryObservations) counts() (initial, updates, purges, late int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, e := range o.main {
		r, ok, err := engine.DecodeWindowResult(e)
		if err != nil || !ok {
			continue
		}
		if string(r.Key) == "k" {
			if r.IsUpdate {
				updates++
			} else {
				initial++
			}
		} else if r.WindowEnd >= 20 {
			purges++
		}
	}
	return initial, updates, purges, len(o.late)
}

type lateRecoverySink struct {
	checkpointTestSource
	observations *lateRecoveryObservations
	late         bool
}

func (*lateRecoverySink) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (s *lateRecoverySink) Write(_ context.Context, e engine.Event) error {
	s.observations.mu.Lock()
	defer s.observations.mu.Unlock()
	if s.late {
		s.observations.late = append(s.observations.late, e)
	} else {
		s.observations.main = append(s.observations.main, e)
	}
	return nil
}

// Ordinary sinks may see replay after recovery. This test checks window state,
// update identity and one late delivery per attempt, not transactional visibility.
func TestClusterWindowLateOutputCheckpointRecovery(t *testing.T) {
	for _, kind := range []string{"tumbling", "sliding", "session"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "coordinator"}, coordinator.NewMemoryStore(), nil, zerolog.Nop())
			coordDone := make(chan error, 1)
			go func() { coordDone <- coord.Run(ctx) }()
			waitFor(t, 2*time.Second, coord.IsReady)
			server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
			if err := server.Listen(); err != nil {
				t.Fatal(err)
			}
			serverDone := make(chan error, 1)
			go func() { serverDone <- server.Serve(ctx) }()
			var phase atomic.Int32
			phase.Store(1)
			var fail, restored atomic.Bool
			var instances atomic.Int32
			observations := &lateRecoveryObservations{}
			registry := worker.NewRegistry()
			registry.RegisterSource("source", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
				return &lateRecoverySource{phase: &phase, fail: &fail, restored: &restored, needsRestore: instances.Add(1) > 1}, nil
			})
			registry.RegisterWindow("window", func(context.Context, []byte, worker.TaskContext) (worker.WindowOperator, error) {
				// Deliberately wrong factory dimensions: the deployment definition must win.
				return engine.NewEventTimeWindowOperator(engine.WindowConfig{Kind: "tumbling", Size: 1000, AggregationID: "count-v1"}, watermarkClusterCount{}, func(r engine.WindowResult) engine.Event {
					return engine.Event{Key: r.Key, Value: r.Value, EventTime: r.WindowEnd}
				})
			})
			for _, name := range []string{"main", "late"} {
				registry.RegisterSink(name, func(context.Context, []byte, worker.TaskContext) (engine.SinkOperator, error) {
					return &lateRecoverySink{observations: observations, late: name == "late"}, nil
				})
			}
			var workers []*worker.Worker
			var done []chan error
			for i := 0; i < 2; i++ {
				replica := &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 4}
				w := worker.NewWithRegistry(worker.Config{WorkerID: fmt.Sprint("worker-", i), CoordinatorAddr: server.Addr(), TaskSlots: 2, CheckpointReplica: replica}, registry, zerolog.Nop())
				workers = append(workers, w)
				ch := make(chan error, 1)
				done = append(done, ch)
				go func() { ch <- w.Run(ctx) }()
			}
			defer func() {
				cancel()
				for _, w := range workers {
					_ = w.Shutdown(context.Background())
				}
				for _, ch := range done {
					<-ch
				}
				_ = server.Shutdown(context.Background())
				<-serverDone
				<-coordDone
			}()
			waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
			definition := &rpc.WindowDefinition{Kind: kind, Size: 10, Slide: 5, Gap: 10, AllowedLateness: 30}
			graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{
				{OperatorID: "source", ClassName: "source", Type: rpc.OperatorTypeSource, Watermark: &rpc.WatermarkConfig{Strategy: "monotonic", EmitInterval: time.Millisecond}},
				{OperatorID: "window", ClassName: "window", Type: rpc.OperatorTypeWindow, Window: definition, LateOutputTag: "late"},
				{OperatorID: "main", ClassName: "main", Type: rpc.OperatorTypeSink},
				{OperatorID: "late", ClassName: "late", Type: rpc.OperatorTypeSink},
			}, Edges: []rpc.EdgeDescriptor{
				{SourceOperatorID: "source", TargetOperatorID: "window", Shuffle: rpc.ShuffleStrategyHash},
				{SourceOperatorID: "window", TargetOperatorID: "main", Shuffle: rpc.ShuffleStrategyForward},
				{SourceOperatorID: "window", TargetOperatorID: "late", Shuffle: rpc.ShuffleStrategyForward, SideOutput: "late"},
			}}
			data, err := protocol.EncodeMsgPack(graph)
			if err != nil {
				t.Fatal(err)
			}
			job, err := coord.SubmitJob("late-recovery", 1, data)
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if kind == "sliding" {
				expected = 2
			}
			waitFor(t, 5*time.Second, func() bool { i, _, _, _ := observations.counts(); return i == expected })
			checkpoint, err := coord.TriggerCheckpoint(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, 5*time.Second, func() bool { j, e := coord.GetJob(job.ID); return e == nil && j.LatestCheckpoint == checkpoint.ID })
			for attempt := 1; attempt <= 2; attempt++ {
				phase.Store(2)
				waitFor(t, 5*time.Second, func() bool { _, u, _, _ := observations.counts(); return u == expected*attempt })
				phase.Store(3)
				waitFor(t, 5*time.Second, func() bool { _, _, p, _ := observations.counts(); return p >= attempt })
				phase.Store(4)
				waitFor(t, 5*time.Second, func() bool { _, _, _, l := observations.counts(); return l == attempt })
				if attempt == 1 {
					phase.Store(1)
					fail.Store(true)
					waitFor(t, 10*time.Second, func() bool { j, e := coord.GetJob(job.ID); return e == nil && j.RestartCount == 1 && restored.Load() })
				}
			}
			// Completing another checkpoint proves barriers reached both routed branches.
			second, err := coord.TriggerCheckpoint(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, 5*time.Second, func() bool { j, e := coord.GetJob(job.ID); return e == nil && j.LatestCheckpoint == second.ID })
			initial, updates, _, late := observations.counts()
			if initial != expected || updates != 2*expected || late != 2 {
				t.Fatalf("recovery delivery counts initial=%d updates=%d late=%d", initial, updates, late)
			}
			observations.mu.Lock()
			defer observations.mu.Unlock()
			for _, e := range observations.main {
				r, ok, err := engine.DecodeWindowResult(e)
				if err != nil || !ok {
					t.Fatalf("invalid main result: %v", err)
				}
				if string(r.Key) == "k" && r.IsUpdate && binary.BigEndian.Uint64(r.Value) != 2 {
					t.Fatalf("restored update has wrong count: %v", r)
				}
			}
			for _, e := range observations.late {
				if string(e.Value) != "expired" || string(e.Key) != "k" || e.EventTime != 1 || string(e.Headers["original"]) != "yes" || len(e.Headers) != 1 {
					t.Fatalf("late record changed: %+v", e)
				}
			}
		})
	}
}
