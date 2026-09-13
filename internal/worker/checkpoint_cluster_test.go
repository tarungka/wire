package worker_test

import (
	"context"
	"errors"
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

type checkpointTestSource struct{}

func (*checkpointTestSource) Open(context.Context) error        { return nil }
func (*checkpointTestSource) Close() error                      { return nil }
func (*checkpointTestSource) Checkpoint(uint64) ([]byte, error) { return []byte("source-offset"), nil }
func (*checkpointTestSource) GenerateWatermark() int64          { return 0 }
func (*checkpointTestSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return []engine.Event{{Value: []byte("record")}}, nil
	}
}

func TestClusterCheckpointRestartsFromReplica(t *testing.T) {
	testClusterCheckpoint(t, false, false, true)
}

func TestClusterCheckpointReplicatesAndCompletes(t *testing.T) {
	testClusterCheckpoint(t, false)
}

func TestClusterCheckpointFailureThreshold(t *testing.T) {
	testClusterCheckpoint(t, true)
}

func TestClusterCheckpointTransactionalAbort(t *testing.T) {
	testClusterCheckpoint(t, true, true)
}

func TestClusterCheckpointTransactionalCommit(t *testing.T) {
	testClusterCheckpoint(t, false, true)
}

func TestClusterCheckpointCoordinatorFailover(t *testing.T) {
	testClusterCheckpoint(t, false, false, false, true)
}

func testClusterCheckpoint(t *testing.T, fail bool, transactional ...bool) {
	t.Helper()
	failover := len(transactional) > 2 && transactional[2]
	timeout := 15 * time.Second
	if failover {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	coordCtx, stopCoordinator := context.WithCancel(ctx)
	defer func() { stopCoordinator() }()
	metadata := coordinator.NewMemoryStore()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "coordinator"}, metadata, nil, zerolog.Nop())
	coordDone := make(chan error, 1)
	go func() { coordDone <- coord.Run(coordCtx) }()
	waitFor(t, 2*time.Second, coord.IsReady)
	server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(coordCtx) }()
	defer func() {
		cancel()
		_ = server.Shutdown(context.Background())
		if err := <-serverDone; err != nil {
			t.Error(err)
		}
	}()
	restart := len(transactional) > 1 && transactional[1]
	var failSource atomic.Bool
	var instances atomic.Int32
	var restoredRead atomic.Bool
	registry := worker.NewRegistry()
	registry.RegisterSource("checkpoint-source", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
		if restart || failover {
			return &restartCheckpointSource{fail: &failSource, needsRestore: instances.Add(1) > 1, readAfterRestore: &restoredRead}, nil
		}
		return &checkpointTestSource{}, nil
	})
	var committed atomic.Uint64
	var aborted atomic.Uint64
	var written atomic.Uint64
	var earlyCommit atomic.Bool
	var jobID atomic.Value
	registry.RegisterSink("checkpoint-sink", func(context.Context, []byte, worker.TaskContext) (engine.SinkOperator, error) {
		return &checkpointTestSink{abort: func() { aborted.Add(1) }, write: func() { written.Add(1) }, commit: func(id uint64) {
			value := jobID.Load()
			if value == nil {
				earlyCommit.Store(true)
				return
			}
			job, err := coord.GetJob(value.(string))
			if err != nil || job.LatestCheckpoint < id {
				earlyCommit.Store(true)
			}
			committed.Store(id)
		}}, nil
	})
	var stores []string
	var workers []*worker.Worker
	var done []chan error
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		stores = append(stores, root)
		replica := &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: root, ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 1}
		taskConfig := engine.DefaultTaskSlotConfig()
		if fail {
			replica.Authorize = func(context.Context, rpc.ReplicateCheckpointRequest) error { return errors.New("replica refused") }
			taskConfig.Checkpoint.MaxConsecutiveFailures = 2
		}
		w := worker.NewWithRegistry(worker.Config{WorkerID: fmt.Sprintf("worker-%d", i), CoordinatorAddr: server.Addr(), TaskSlots: 1, TaskSlot: &taskConfig, CheckpointReplica: replica}, registry, zerolog.Nop())
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
		<-coordDone
	}()
	waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
	graphSpec := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{{OperatorID: "source", ClassName: "checkpoint-source", Type: rpc.OperatorTypeSource}}}
	if len(transactional) > 0 && transactional[0] {
		graphSpec.Operators = append(graphSpec.Operators, rpc.OperatorDescriptor{OperatorID: "sink", ClassName: "checkpoint-sink", Type: rpc.OperatorTypeSink})
		graphSpec.Edges = append(graphSpec.Edges, rpc.EdgeDescriptor{SourceOperatorID: "source", TargetOperatorID: "sink"})
	}
	graph, err := protocol.EncodeMsgPack(graphSpec)
	if err != nil {
		t.Fatal(err)
	}
	job, err := coord.SubmitJob("checkpoint", 1, graph)
	if err != nil {
		t.Fatal(err)
	}
	jobID.Store(job.ID)
	waitFor(t, 5*time.Second, func() bool {
		current, err := coord.GetJob(job.ID)
		return err == nil && current.Status == coordinator.JobRunning
	})
	checkpoint, err := coord.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fail {
		waitFor(t, 3*time.Second, func() bool {
			data, err := metadata.Get(coordinator.CheckpointKey(job.ID, checkpoint.ID))
			if err != nil {
				return false
			}
			var state coordinator.CheckpointMeta
			return protocol.DecodeMsgPack(data, &state) == nil && state.Status == coordinator.CheckpointAborted
		})
		current, err := coord.GetJob(job.ID)
		if err != nil || current.Status != coordinator.JobRunning {
			t.Fatalf("first failure killed job: %+v, %v", current, err)
		}
		if len(transactional) > 0 && transactional[0] {
			waitFor(t, 3*time.Second, func() bool { return aborted.Load() > 0 })
			before := written.Load()
			waitFor(t, 3*time.Second, func() bool { return written.Load() > before })
			if committed.Load() != 0 {
				t.Fatal("failed checkpoint committed transaction")
			}
		}
		if _, err := coord.TriggerCheckpoint(job.ID); err != nil {
			t.Fatal(err)
		}
		waitFor(t, 3*time.Second, func() bool {
			current, err := coord.GetJob(job.ID)
			return err == nil && (current.Status == coordinator.JobFailing || current.Status == coordinator.JobFailed)
		})
		return
	}
	waitFor(t, 4*time.Second, func() bool {
		current, err := coord.GetJob(job.ID)
		return err == nil && current.LatestCheckpoint == checkpoint.ID
	})
	if len(transactional) > 0 && transactional[0] {
		waitFor(t, 3*time.Second, func() bool { return committed.Load() == checkpoint.ID })
		if earlyCommit.Load() {
			t.Fatal("sink committed before global completion")
		}
	}
	for taskID, owner := range checkpoint.Tasks {
		index := 1
		if owner == "worker-1" {
			index = 0
		}
		store, err := engine.NewFileCheckpointStore(stores[index])
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.Get(ctx, job.ID, taskID, checkpoint.ID, checkpoint.EpochID)
		if err != nil || string(snapshot.Source) != "source-offset" {
			t.Fatalf("remote snapshot: %+v, %v", snapshot, err)
		}
	}
	if failover {
		oldEpoch := coord.CurrentEpoch()
		address := server.Addr()
		stopCoordinator()
		_ = server.Shutdown(context.Background())
		<-serverDone
		<-coordDone
		coordCtx, stopCoordinator = context.WithCancel(ctx)
		defer stopCoordinator()
		coord = coordinator.New(coordinator.CoordinatorConfig{NodeID: "replacement"}, metadata, nil, zerolog.Nop())
		coordDone = make(chan error, 1)
		go func() { coordDone <- coord.Run(coordCtx) }()
		waitFor(t, 2*time.Second, coord.IsReady)
		if coord.CurrentEpoch() <= oldEpoch {
			t.Fatal("replacement epoch did not advance")
		}
		server = coordinator.NewTransportServer(coord, address, zerolog.Nop())
		if err := server.Listen(); err != nil {
			t.Fatal(err)
		}
		serverDone = make(chan error, 1)
		go func() { serverDone <- server.Serve(coordCtx) }()
		waitFor(t, 45*time.Second, func() bool {
			current, err := coord.GetJob(job.ID)
			return err == nil && current.Status == coordinator.JobRunning && current.RestartCount == 1 && restoredRead.Load()
		})
	}
	if restart {
		failSource.Store(true)
		waitFor(t, 8*time.Second, func() bool {
			current, err := coord.GetJob(job.ID)
			return err == nil && current.Status == coordinator.JobRunning && current.RestartCount == 1 && restoredRead.Load()
		})
	}
}

type restartCheckpointSource struct {
	checkpointTestSource
	fail             *atomic.Bool
	needsRestore     bool
	restored         bool
	readAfterRestore *atomic.Bool
}

func (s *restartCheckpointSource) RestoreCheckpoint(data []byte) error {
	if string(data) != "source-offset" {
		return errors.New("incorrect restored source offset")
	}
	s.restored = true
	return nil
}
func (s *restartCheckpointSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	if s.needsRestore {
		if !s.restored {
			return nil, errors.New("source read before restore")
		}
		s.readAfterRestore.Store(true)
	} else if s.fail.Load() {
		return nil, errors.New("injected source failure")
	}
	return s.checkpointTestSource.ReadBatch(ctx)
}

type checkpointTestSink struct {
	commit func(uint64)
	abort  func()
	write  func()
}

func (*checkpointTestSink) Open(context.Context) error                  { return nil }
func (*checkpointTestSink) Close() error                                { return nil }
func (*checkpointTestSink) Checkpoint(uint64) ([]byte, error)           { return []byte("sink-state"), nil }
func (s *checkpointTestSink) Write(context.Context, engine.Event) error { s.write(); return nil }
func (*checkpointTestSink) BeginTransaction(context.Context) error      { return nil }
func (*checkpointTestSink) PreCommit(context.Context, uint64) error     { return nil }
func (s *checkpointTestSink) Commit(_ context.Context, id uint64) error { s.commit(id); return nil }
func (s *checkpointTestSink) Abort(context.Context) error               { s.abort(); return nil }
