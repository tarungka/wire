package worker_test

import (
	"context"
	"errors"
	"fmt"
	"os"
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

func TestClusterCheckpointRestartsAfterWorkerLoss(t *testing.T) {
	testClusterCheckpoint(t, false, false, false, false, false, false, false, true)
}

func TestClusterCheckpointRestartsFromReplica(t *testing.T) {
	testClusterCheckpoint(t, false, false, true)
}

func TestClusterCheckpointFallsBackFromMissingArchive(t *testing.T) {
	testClusterCheckpoint(t, false, false, true, false, true)
}

func TestClusterCheckpointFallsBackFromCorruptArchive(t *testing.T) {
	testClusterCheckpoint(t, false, false, true, false, true, true)
}

func TestClusterCheckpointSkipsFourMissingArchives(t *testing.T) {
	testClusterCheckpoint(t, false, false, true, false, true, false, true)
}

func TestClusterCheckpointRefusesTransactionalFallback(t *testing.T) {
	testClusterCheckpoint(t, false, true, true, false, true)
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
	if testing.Short() {
		t.Skip("coordinator contact-loss policy requires 30 seconds; covered by full and integration suites")
	}
	testClusterCheckpoint(t, false, false, false, true)
}

func testClusterCheckpoint(t *testing.T, fail bool, transactional ...bool) {
	t.Helper()
	failover := len(transactional) > 2 && transactional[2]
	workerLoss := len(transactional) > 6 && transactional[6]
	fallback := len(transactional) > 3 && transactional[3]
	timeout := 15 * time.Second
	if fallback {
		timeout = 60 * time.Second
	}
	if failover {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	coordCtx, stopCoordinator := context.WithCancel(ctx)
	defer func() { stopCoordinator() }()
	metadata := coordinator.NewMemoryStore()
	coordinatorConfig := coordinator.CoordinatorConfig{NodeID: "coordinator"}
	if workerLoss {
		coordinatorConfig.WorkerTimeout = 500 * time.Millisecond
		coordinatorConfig.HeartbeatInterval = 50 * time.Millisecond
	}
	coord := coordinator.New(coordinatorConfig, metadata, nil, zerolog.Nop())
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
		if restart || failover || workerLoss {
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
		workerConfig := worker.Config{WorkerID: fmt.Sprintf("worker-%d", i), CoordinatorAddr: server.Addr(), TaskSlots: 1, TaskSlot: &taskConfig, CheckpointReplica: replica}
		if workerLoss {
			workerConfig.HeartbeatInterval = 50 * time.Millisecond
			workerConfig.HeartbeatTimeout = 500 * time.Millisecond
		}
		w := worker.NewWithRegistry(workerConfig, registry, zerolog.Nop())
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
		if len(transactional) > 0 && transactional[0] {
			waitFor(t, 3*time.Second, func() bool { return aborted.Load() > 0 })
			// Transaction rollback requires source replay even when ordinary
			// checkpoint failure policy would tolerate the failed snapshot.
			// There is no completed checkpoint in this fixture to restore.
			waitFor(t, 3*time.Second, func() bool {
				current, err := coord.GetJob(job.ID)
				return err == nil && (current.Status == coordinator.JobFailing || current.Status == coordinator.JobFailed)
			})
			if committed.Load() != 0 {
				t.Fatal("failed checkpoint committed transaction")
			}
			return
		}
		current, err := coord.GetJob(job.ID)
		if err != nil || current.Status != coordinator.JobRunning {
			t.Fatalf("first non-transactional checkpoint failure killed job: %+v, %v", current, err)
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
	rawManifest, err := metadata.Get(coordinator.CheckpointManifestKey(job.ID, checkpoint.ID))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := engine.UnmarshalCheckpointMetadata(rawManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.ValidateComplete(); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Tasks) != len(checkpoint.Tasks) {
		t.Fatal("completed manifest lost task inventory")
	}
	for _, task := range manifest.Tasks {
		if task.StateSHA256["checkpoint.archive"] == "" || task.StateSizeBytes <= 0 || len(task.SourceOffsets) == 0 {
			t.Fatal("manifest lost durable archive inventory")
		}
	}
	if len(transactional) > 0 && transactional[0] {
		if len(manifest.SinkTxns) != 1 || manifest.SinkTxns[0].TransactionState != "PRE_COMMITTED" {
			t.Fatal("manifest missing prepared transaction")
		}
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
	if workerLoss {
		var owner string
		for _, id := range checkpoint.Tasks {
			owner = id
			break
		}
		victim := 0
		if owner == "worker-1" {
			victim = 1
		}
		lostAt := time.Now()
		if err := workers[victim].Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		waitFor(t, time.Second+coordinatorConfig.WorkerTimeout, func() bool {
			for _, w := range coord.ListWorkers() {
				if w.ID == owner {
					return w.Lost
				}
			}
			return false
		})
		if time.Since(lostAt) > coordinatorConfig.WorkerTimeout+time.Second {
			t.Fatal("worker loss detection exceeded budget")
		}
		waitFor(t, 5*time.Second, func() bool {
			current, err := coord.GetJob(job.ID)
			return err == nil && current.Status == coordinator.JobRunning && current.RestartCount == 1 && restoredRead.Load()
		})
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
		expectedRestarts := 1
		if fallback {
			badCount := 1
			if len(transactional) > 5 && transactional[5] {
				badCount = 4
			}
			for bad := 0; bad < badCount; bad++ {
				latest, err := coord.TriggerCheckpoint(job.ID)
				if err != nil {
					t.Fatal(err)
				}
				waitFor(t, 4*time.Second, func() bool {
					current, err := coord.GetJob(job.ID)
					return err == nil && current.LatestCheckpoint == latest.ID
				})
				for taskID, owner := range latest.Tasks {
					index := 1
					if owner == "worker-1" {
						index = 0
					}
					store, err := engine.NewFileCheckpointStore(stores[index])
					if err != nil {
						t.Fatal(err)
					}
					archive, err := store.OpenArchive(ctx, job.ID, taskID, latest.ID, latest.EpochID)
					if err != nil {
						t.Fatal(err)
					}
					name := archive.Name()
					_ = archive.Close()
					if len(transactional) > 4 && transactional[4] {
						file, err := os.OpenFile(name, os.O_WRONLY, 0600)
						if err != nil {
							t.Fatal(err)
						}
						_, err = file.WriteAt([]byte("X"), 0)
						closeErr := file.Close()
						if err != nil {
							t.Fatal(err)
						}
						if closeErr != nil {
							t.Fatal(closeErr)
						}
					} else if err := os.Remove(name); err != nil {
						t.Fatal(err)
					}
				}
			}
			expectedRestarts = 1 + badCount
		}
		if fallback && transactional[0] {
			waitFor(t, 4*time.Second, func() bool {
				current, _ := coord.GetJob(job.ID)
				return current != nil && committed.Load() == current.LatestCheckpoint
			})
		}
		failSource.Store(true)
		if fallback && transactional[0] {
			waitFor(t, 20*time.Second, func() bool {
				current, _ := coord.GetJob(job.ID)
				return current != nil && current.Status == coordinator.JobFailed
			})
			if restoredRead.Load() {
				t.Fatal("replayed past committed transaction")
			}
			return
		}
		waitFor(t, 45*time.Second, func() bool {
			current, err := coord.GetJob(job.ID)
			return err == nil && current.Status == coordinator.JobRunning && current.RestartCount == expectedRestarts && current.LatestCheckpoint == checkpoint.ID && restoredRead.Load()
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
