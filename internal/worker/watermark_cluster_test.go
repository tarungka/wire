package worker_test

import (
	"context"
	"encoding/binary"
	"fmt"
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

type watermarkClusterSource struct {
	checkpointTestSource
	sent        bool
	strategy    string
	windowCount *atomic.Uint64
	sentLate    bool
}

func (s *watermarkClusterSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	if !s.sent {
		s.sent = true
		times := []int64{2, 8, 30}
		if s.strategy == "bounded-ooo" {
			times = []int64{8, 2, 30}
		}
		events := make([]engine.Event, 0, len(times))
		for _, timestamp := range times {
			events = append(events, engine.Event{Key: []byte("key"), EventTime: timestamp})
		}
		return events, nil
	}
	if s.windowCount != nil && s.windowCount.Load() >= 2 && !s.sentLate {
		s.sentLate = true
		// The [0,10) window has fired. Timestamp 2 must be dropped;
		// timestamp 50 advances the watermark and closes the record at 30.
		return []engine.Event{{Key: []byte("key"), EventTime: 2}, {Key: []byte("key"), EventTime: 50}}, nil
	}
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return []engine.Event{}, nil
	}
}

type watermarkClusterCount struct{}

func (watermarkClusterCount) CreateAccumulator() []byte { return make([]byte, 8) }
func (watermarkClusterCount) Add(acc []byte, _ engine.Event) []byte {
	binary.BigEndian.PutUint64(acc, binary.BigEndian.Uint64(acc)+1)
	return acc
}
func (watermarkClusterCount) GetResult(acc []byte) []byte { return acc }
func (watermarkClusterCount) Merge(a, b []byte) []byte {
	binary.BigEndian.PutUint64(a, binary.BigEndian.Uint64(a)+binary.BigEndian.Uint64(b))
	return a
}

type watermarkClusterSink struct {
	checkpointTestSource
	count *atomic.Uint64
}

func (s *watermarkClusterSink) Write(_ context.Context, event engine.Event) error {
	if len(event.Value) != 8 {
		return fmt.Errorf("invalid window result")
	}
	s.count.Add(binary.BigEndian.Uint64(event.Value))
	return nil
}

func TestClusterWatermarkStrategiesCloseWindows(t *testing.T) {
	for _, strategy := range []string{"bounded-ooo", "monotonic", "ingestion-time", "idle-input", "late-record", "restore"} {
		t.Run(strategy, func(t *testing.T) {
			restore := strategy == "restore"
			idleInput := strategy == "idle-input"
			lateRecord := strategy == "late-record"
			if idleInput || lateRecord || restore {
				strategy = "bounded-ooo"
			}
			workerCount := 2
			if idleInput {
				workerCount++
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
			var count atomic.Uint64
			var failSource atomic.Bool
			var advanceSource atomic.Bool
			var committed atomic.Uint64
			var instances atomic.Int32
			var restored atomic.Bool
			registry := worker.NewRegistry()
			registry.RegisterSource("source", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
				source := &watermarkClusterSource{strategy: strategy}
				if restore {
					return &watermarkRestoreSource{watermarkClusterSource: source, advance: &advanceSource, fail: &failSource, needsRestore: instances.Add(1) > 1, restoredRead: &restored}, nil
				}
				if lateRecord {
					source.windowCount = &count
				}
				return source, nil
			})
			registry.RegisterSource("idle", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
				return &watermarkClusterSource{sent: true}, nil
			})
			registry.RegisterWindow("window", func(context.Context, []byte, worker.TaskContext) (worker.WindowOperator, error) {
				return engine.NewEventTimeWindowOperator(engine.WindowConfig{Kind: "tumbling", Size: 10, AggregationID: "count-v1"}, watermarkClusterCount{}, func(result engine.WindowResult) engine.Event {
					return engine.Event{Key: result.Key, Value: result.Value, EventTime: result.WindowEnd}
				})
			})
			registry.RegisterSink("sink", func(context.Context, []byte, worker.TaskContext) (engine.SinkOperator, error) {
				if restore {
					return &watermarkTransactionalSink{watermarkClusterSink: &watermarkClusterSink{count: &count}, committed: &committed}, nil
				}
				return &watermarkClusterSink{count: &count}, nil
			})
			var workers []*worker.Worker
			var done []chan error
			for i := 0; i < workerCount; i++ {
				var replica *worker.CheckpointReplicaConfig
				if restore {
					replica = &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 2}
				}
				w := worker.NewWithRegistry(worker.Config{CheckpointReplica: replica, WorkerID: fmt.Sprint("worker-", i), CoordinatorAddr: server.Addr(), TaskSlots: 1}, registry, zerolog.Nop())
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
			waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == workerCount })
			cfg := &rpc.WatermarkConfig{Strategy: strategy, EmitInterval: 5 * time.Millisecond}
			if strategy == "bounded-ooo" {
				tolerance := 5 * time.Millisecond
				cfg.MaxOOO = &tolerance
			}
			graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{{OperatorID: "source", ClassName: "source", Type: rpc.OperatorTypeSource, Watermark: cfg}, {OperatorID: "window", ClassName: "window", Type: rpc.OperatorTypeWindow}, {OperatorID: "sink", ClassName: "sink", Type: rpc.OperatorTypeSink}}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "window", Shuffle: rpc.ShuffleStrategyHash}, {SourceOperatorID: "window", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyForward}}}
			if idleInput {
				graph.Operators = append(graph.Operators, rpc.OperatorDescriptor{OperatorID: "idle", ClassName: "idle", Type: rpc.OperatorTypeSource, Watermark: &rpc.WatermarkConfig{Strategy: "monotonic", IdleTimeout: 50 * time.Millisecond}})
				graph.Edges = append(graph.Edges, rpc.EdgeDescriptor{SourceOperatorID: "idle", TargetOperatorID: "window", Shuffle: rpc.ShuffleStrategyHash})
			}
			data, err := protocol.EncodeMsgPack(graph)
			if err != nil {
				t.Fatal(err)
			}
			job, err := coord.SubmitJob("watermarks", 1, data)
			if err != nil {
				t.Fatal(err)
			}
			want := uint64(2)
			if strategy == "ingestion-time" || lateRecord {
				want = 3
			}
			waitFor(t, 5*time.Second, func() bool { return count.Load() >= want })
			if got := count.Load(); got != want {
				t.Fatalf("window count=%d want=%d", got, want)
			}
			if restore {
				checkpoint, err := coord.TriggerCheckpoint(job.ID)
				if err != nil {
					t.Fatal(err)
				}
				waitFor(t, 5*time.Second, func() bool {
					current, err := coord.GetJob(job.ID)
					return err == nil && current.LatestCheckpoint == checkpoint.ID
				})
				waitFor(t, 3*time.Second, func() bool { return committed.Load() == 2 })
				advanceSource.Store(true)
				waitFor(t, 3*time.Second, func() bool { return count.Load() == 3 })
				if committed.Load() != 2 {
					t.Fatal("uncheckpointed window became visible")
				}
				failSource.Store(true)
				waitFor(t, 8*time.Second, func() bool {
					current, err := coord.GetJob(job.ID)
					return err == nil && current.RestartCount == 1 && restored.Load() && count.Load() >= 4
				})
				if got := count.Load(); got != 4 {
					t.Fatalf("window attempts=%d want=4 (one replay)", got)
				}
				if _, err := coord.TriggerCheckpoint(job.ID); err != nil {
					t.Fatal(err)
				}
				waitFor(t, 5*time.Second, func() bool { return committed.Load() >= 3 })
				if committed.Load() != 3 {
					t.Fatalf("duplicate committed window: %d", committed.Load())
				}
			}
		})
	}
}

// Count records every write attempt; the transactional wrapper tracks visibility.
func (*watermarkClusterSink) Checkpoint(uint64) ([]byte, error) { return nil, nil }

type watermarkRestoreSource struct {
	*watermarkClusterSource
	fail         *atomic.Bool
	advance      *atomic.Bool
	needsRestore bool
	restored     bool
	restoredRead *atomic.Bool
	advanced     bool
}

func (s *watermarkRestoreSource) Checkpoint(uint64) ([]byte, error) {
	if !s.sent {
		return []byte{0}, nil
	}
	return []byte{1}, nil
}
func (s *watermarkRestoreSource) RestoreCheckpoint(data []byte) error {
	if len(data) != 1 || data[0] != 1 {
		return fmt.Errorf("incorrect restored source offset: %v", data)
	}
	s.sent = true
	s.restored = true
	return nil
}
func (s *watermarkRestoreSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	if s.needsRestore {
		if !s.restored {
			return nil, fmt.Errorf("source read before restore")
		}
		s.restoredRead.Store(true)
		if !s.advanced {
			s.advanced = true
			return []engine.Event{{Key: []byte("key"), EventTime: 50}}, nil
		}
	} else if s.fail.Load() {
		return nil, fmt.Errorf("injected source failure")
	} else if s.advance.Load() && !s.advanced {
		s.advanced = true
		return []engine.Event{{Key: []byte("key"), EventTime: 50}}, nil
	}
	return s.watermarkClusterSource.ReadBatch(ctx)
}

type watermarkTransactionalSink struct {
	*watermarkClusterSink
	committed *atomic.Uint64
	pending   uint64
	prepared  uint64
}

func (s *watermarkTransactionalSink) Write(ctx context.Context, event engine.Event) error {
	if err := s.watermarkClusterSink.Write(ctx, event); err != nil {
		return err
	}
	s.pending += binary.BigEndian.Uint64(event.Value)
	return nil
}
func (*watermarkTransactionalSink) BeginTransaction(context.Context) error { return nil }
func (s *watermarkTransactionalSink) PreCommit(context.Context, uint64) error {
	s.prepared = s.pending
	s.pending = 0
	return nil
}
func (s *watermarkTransactionalSink) Commit(context.Context, uint64) error {
	s.committed.Add(s.prepared)
	s.prepared = 0
	return nil
}
func (s *watermarkTransactionalSink) Abort(context.Context) error {
	s.pending = 0
	s.prepared = 0
	return nil
}

func (*watermarkTransactionalSink) RecoverTransactions(context.Context, engine.TransactionRecovery) error {
	return nil
}
