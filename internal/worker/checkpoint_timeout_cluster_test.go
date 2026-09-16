package worker_test

import (
	"context"
	"fmt"
	"strconv"
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

type timeoutBarrierSource struct {
	slow     bool
	release  <-chan struct{}
	emitted  *atomic.Int64
	captured *atomic.Uint64
}

func (*timeoutBarrierSource) Open(context.Context) error { return nil }
func (*timeoutBarrierSource) Close() error               { return nil }
func (*timeoutBarrierSource) GenerateWatermark() int64   { return 0 }
func (s *timeoutBarrierSource) Checkpoint(id uint64) ([]byte, error) {
	if !s.slow {
		s.captured.Store(id)
	}
	return nil, nil
}
func (s *timeoutBarrierSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	if s.slow {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	if s.slow {
		return []engine.Event{{Key: []byte("slow")}}, nil
	}
	id := s.emitted.Add(1) - 1
	return []engine.Event{{Key: []byte("key"), Value: []byte(strconv.FormatInt(id, 10))}}, nil
}

type timeoutRecordSink struct {
	mu     sync.Mutex
	values []string
}

func (*timeoutRecordSink) Open(context.Context) error        { return nil }
func (*timeoutRecordSink) Close() error                      { return nil }
func (*timeoutRecordSink) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (s *timeoutRecordSink) Write(_ context.Context, event engine.Event) error {
	if string(event.Key) == "slow" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = append(s.values, string(event.Value))
	return nil
}
func (s *timeoutRecordSink) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.values) }

func TestClusterAlignmentTimeoutRecovery(t *testing.T)         { testClusterAlignmentTimeout(t, false) }
func TestClusterAlignmentTimeoutFailureThreshold(t *testing.T) { testClusterAlignmentTimeout(t, true) }
func testClusterAlignmentTimeout(t *testing.T, failJob bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	metadata := coordinator.NewMemoryStore()
	cfg := coordinator.CoordinatorConfig{NodeID: "coordinator", CheckpointTimeout: time.Second}
	if failJob {
		cfg.CheckpointMaxConsecutiveFailures = 2
	}
	coord := coordinator.New(cfg, metadata, nil, zerolog.Nop())
	coordDone := make(chan error, 1)
	go func() { coordDone <- coord.Run(ctx) }()
	waitFor(t, 2*time.Second, coord.IsReady)
	server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(ctx) }()
	var emitted atomic.Int64
	var captured atomic.Uint64
	release := make(chan struct{})
	sink := &timeoutRecordSink{}
	registry := worker.NewRegistry()
	registry.RegisterSource("timeout-source", func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.SourceOperator, error) {
		return &timeoutBarrierSource{slow: tc.SubtaskIndex == 1, release: release, emitted: &emitted, captured: &captured}, nil
	})
	registry.RegisterSink("timeout-sink", func(context.Context, []byte, worker.TaskContext) (engine.SinkOperator, error) { return sink, nil })
	var workers []*worker.Worker
	var done []chan error
	defer func() {
		cancel()
		_ = server.Shutdown(context.Background())
		for _, w := range workers {
			_ = w.Shutdown(context.Background())
		}
		for _, ch := range done {
			<-ch
		}
		<-serverDone
		<-coordDone
	}()
	for i := 0; i < 2; i++ {
		taskCfg := engine.DefaultTaskSlotConfig()
		taskCfg.AlignmentBufferSize = 8
		w := worker.NewWithRegistry(worker.Config{WorkerID: fmt.Sprintf("worker-%d", i), CoordinatorAddr: server.Addr(), TaskSlots: 2, TaskSlot: &taskCfg, CheckpointReplica: &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 2}}, registry, zerolog.Nop())
		workers = append(workers, w)
		ch := make(chan error, 1)
		done = append(done, ch)
		go func() { ch <- w.Run(ctx) }()
	}
	waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
	graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{{OperatorID: "source", ClassName: "timeout-source", Type: rpc.OperatorTypeSource, Parallelism: 2}, {OperatorID: "sink", ClassName: "timeout-sink", Type: rpc.OperatorTypeSink, Parallelism: 1}}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyHash}}}
	raw, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		t.Fatal(err)
	}
	job, err := coord.SubmitJob("alignment-timeout", 1, raw)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		job, err := coord.GetJob(job.ID)
		return err == nil && job.Status == coordinator.JobRunning
	})
	waitFor(t, 2*time.Second, func() bool { return sink.count() > 2 })
	first, err := coord.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return captured.Load() == first.ID })
	// The fast input continues after its barrier while the other input withholds
	// its barrier. A small alignment buffer also exercises cancellation of waits.
	waitFor(t, 2*time.Second, func() bool { return emitted.Load() > int64(sink.count()+8) })
	waitFor(t, 4*time.Second, func() bool {
		raw, err := metadata.Get(coordinator.CheckpointKey(job.ID, first.ID))
		if err != nil {
			return false
		}
		var cp coordinator.CheckpointMeta
		return protocol.DecodeMsgPack(raw, &cp) == nil && cp.Status == coordinator.CheckpointAborted
	})
	target := emitted.Load()
	waitFor(t, 3*time.Second, func() bool { return int64(sink.count()) >= target })
	if !failJob {
		close(release)
	}
	next, err := coord.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failJob {
		waitFor(t, 5*time.Second, func() bool {
			current, err := coord.GetJob(job.ID)
			return err == nil && (current.Status == coordinator.JobFailing || current.Status == coordinator.JobFailed)
		})
		current, _ := coord.GetJob(job.ID)
		if current.CheckpointFailures != 2 || current.ConsecutiveCheckpointFailures != 2 {
			t.Fatalf("wrong policy outcome: %+v", current)
		}
	} else {
		waitFor(t, 5*time.Second, func() bool {
			current, err := coord.GetJob(job.ID)
			return err == nil && current.LatestCheckpoint == next.ID
		})
		current, _ := coord.GetJob(job.ID)
		if current.Status != coordinator.JobRunning || current.RestartCount != 0 || current.ConsecutiveCheckpointFailures != 0 {
			t.Fatalf("recovery restarted job or did not reset failures: %+v", current)
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for i, value := range sink.values {
		if value != strconv.Itoa(i) {
			t.Fatalf("record loss/reordering at %d: %s", i, value)
		}
	}
}
