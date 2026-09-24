package sdk

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/jobcli"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

// lifecycleCluster runs the production HTTP, RPC, scheduler and public worker
// API. Each test owns and joins its services and can inspect coordinator state.
func lifecycleCluster(t *testing.T, registry *WorkerRegistry, allowRemoval ...bool) (context.Context, *coordinator.Coordinator, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	store := coordinator.NewMemoryStore()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "lifecycle", WorkerTimeout: time.Second, HeartbeatInterval: 100 * time.Millisecond}, store, nil, zerolog.Nop())
	var joined sync.WaitGroup
	start := func(fn func()) { joined.Add(1); go func() { defer joined.Done(); fn() }() }
	rpcServer := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	httpServer := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
	t.Cleanup(func() {
		cancel()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = httpServer.Shutdown(shutdown)
		_ = rpcServer.Shutdown(shutdown)
		joined.Wait()
		_ = store.Close()
	})
	start(func() { _ = coord.Run(ctx) })
	lifecycleWait(t, ctx, coord.IsReady)
	if err := rpcServer.Listen(); err != nil {
		t.Fatal(err)
	}
	start(func() { _ = rpcServer.Serve(ctx) })
	if err := httpServer.Listen(); err != nil {
		t.Fatal(err)
	}
	start(func() { _ = httpServer.Serve() })
	for i := 0; i < 2; i++ {
		cfg := WorkerConfig{WorkerID: fmt.Sprint("lifecycle-", i), CoordinatorAddr: rpcServer.Addr(), TaskSlots: 4, HeartbeatInterval: 100 * time.Millisecond, HeartbeatTimeout: time.Second, CheckpointDirectory: t.TempDir()}
		start(func() {
			if err := RunWorker(ctx, cfg, registry); err != nil && ctx.Err() == nil {
				if len(allowRemoval) == 0 || !allowRemoval[0] || !errors.Is(err, worker.ErrCoordinatorContactLost) {
					t.Error(err)
				}
			}
		})
	}
	lifecycleWait(t, ctx, func() bool { return len(coord.ListWorkers()) == 2 })
	return ctx, coord, "http://" + httpServer.Addr()
}

func lifecycleWait(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal("lifecycle condition timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

type cancellationSource struct{ closing, closed, release chan struct{} }

func (*cancellationSource) Open(context.Context) error { return nil }
func (*cancellationSource) GenerateWatermark() int64   { return 0 }
func (*cancellationSource) ReadBatch(ctx context.Context) ([]Event, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s *cancellationSource) Close() error {
	close(s.closing)
	<-s.release
	close(s.closed)
	return nil
}

func TestCLICancelWaitsForWorkerTeardown(t *testing.T) {
	source := &cancellationSource{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(source.release) })
	registry := NewWorkerRegistry()
	registry.RegisterSource("blocked", func(context.Context, []byte, WorkerTaskContext) (Source, error) { return source, nil })
	registry.RegisterSink("discard", func(context.Context, []byte, WorkerTaskContext) (Sink, error) { return &collectSink{}, nil })
	ctx, coord, url := lifecycleCluster(t, registry)
	env := New().SetMode(Cluster).SetCoordinator(url)
	env.AddSourceNamed("source", "blocked", nil).AddSinkNamed("sink", "discard", nil)
	result := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "cancel-lifecycle"); result <- err }()
	var jobID string
	lifecycleWait(t, ctx, func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) != 1 || jobs[0].Status != coordinator.JobRunning {
			return false
		}
		jobID = jobs[0].ID
		return true
	})
	var response bytes.Buffer
	if err := jobcli.Run(ctx, []string{"jobs", "cancel", jobID, "--coordinator", url}, &response, &response); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.closing:
	case <-ctx.Done():
		t.Fatal("worker did not cancel the blocked read")
	}
	job, err := coord.GetJob(jobID)
	if err != nil || job.Status != coordinator.JobCanceling {
		t.Fatalf("job finished before Close returned: %+v %v", job, err)
	}
	release.Do(func() { close(source.release) })
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled job reported successful execution")
		}
	case <-ctx.Done():
		t.Fatal("SDK did not observe terminal cancellation")
	}
	select {
	case <-source.closed:
	default:
		t.Fatal("terminal cancellation preceded connector teardown")
	}
	response.Reset()
	if err := jobcli.Run(ctx, []string{"jobs", "get", jobID, "--coordinator", url}, &response, &response); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Bytes(), &status); err != nil || status.Status != "CANCELED" {
		t.Fatalf("CLI status: %s, %v", response.String(), err)
	}
}

type pauseReplaySource struct {
	offset   atomic.Uint64
	release  <-chan struct{}
	restored chan<- uint64
	closed   *atomic.Int32
}

func (*pauseReplaySource) Open(context.Context) error { return nil }
func (s *pauseReplaySource) Close() error             { s.closed.Add(1); return nil }
func (*pauseReplaySource) GenerateWatermark() int64   { return 0 }
func (s *pauseReplaySource) ReadBatch(ctx context.Context) ([]Event, error) {
	switch s.offset.Load() {
	case 0:
		s.offset.Store(1)
		return []Event{{Key: []byte("key"), Value: []byte("first"), EventTime: 1}}, nil
	case 1:
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.release:
			s.offset.Store(2)
			return []Event{{Key: []byte("key"), Value: []byte("second"), EventTime: 2}}, nil
		case <-time.After(5 * time.Millisecond):
			return []Event{}, nil
		}
	default:
		return nil, nil
	}
}
func (s *pauseReplaySource) Checkpoint(uint64) ([]byte, error) {
	return binary.BigEndian.AppendUint64(nil, s.offset.Load()), nil
}
func (s *pauseReplaySource) RestoreOffset(_ context.Context, state []byte) error {
	if len(state) != 8 {
		return fmt.Errorf("invalid offset")
	}
	offset := binary.BigEndian.Uint64(state)
	s.offset.Store(offset)
	s.restored <- offset
	return nil
}

func TestCLIPauseResumeRestoresOffsetsAndManagedState(t *testing.T) { testCLIPauseResume(t, false) }

func TestCLIPauseResumeTransactionalCommitResponseLoss(t *testing.T) { testCLIPauseResume(t, true) }

func testCLIPauseResume(t *testing.T, transactional bool) {
	release := make(chan struct{})
	restored := make(chan uint64, 2)
	var closed atomic.Int32
	registry := NewWorkerRegistry()
	registry.RegisterSource("replay", func(context.Context, []byte, WorkerTaskContext) (Source, error) {
		return &pauseReplaySource{release: release, restored: restored, closed: &closed}, nil
	})
	registry.RegisterKeyBy("key", func(context.Context, []byte, WorkerTaskContext) (KeySelector, error) {
		return func(e Event) ([]byte, error) { return e.Key, nil }, nil
	})
	registry.RegisterProcess("count", func(context.Context, []byte, WorkerTaskContext) (ProcessDefinition, error) {
		return ProcessDefinition{Process: func(ctx ProcessContext, e Event) ([]Event, error) {
			state := ctx.GetState("count")
			n, err := state.ValueInt64()
			if err != nil {
				return nil, err
			}
			if err := state.SetInt64(n + 1); err != nil {
				return nil, err
			}
			e.Value = []byte(fmt.Sprint(n + 1))
			return []Event{e}, nil
		}}, nil
	})
	sink := &collectSink{}
	ledger := &pauseTransactionLedger{prepared: make(map[uint64][]string), committed: make(map[uint64]bool), loseResponse: true}
	registry.RegisterSink("collect", func(context.Context, []byte, WorkerTaskContext) (Sink, error) {
		if transactional {
			return &pauseTransactionSink{ledger: ledger, observed: sink}, nil
		}
		return sink, nil
	})
	ctx, coord, url := lifecycleCluster(t, registry)
	env := New().SetMode(Cluster).SetCoordinator(url)
	env.AddSourceNamed("source", "replay", nil).KeyByNamed("key", "key", nil).ProcessNamed("count", "count", nil).AddSinkNamed("sink", "collect", nil)
	done := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "pause-resume"); done <- err }()
	var jobID string
	lifecycleWait(t, ctx, func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) != 1 || jobs[0].Status != coordinator.JobRunning || len(sink.Events()) != 1 {
			return false
		}
		jobID = jobs[0].ID
		return true
	})
	var response bytes.Buffer
	if err := jobcli.Run(ctx, []string{"jobs", "pause", jobID, "--coordinator", url}, &response, &response); err != nil {
		t.Fatal(err)
	}
	lifecycleWait(t, ctx, func() bool { job, err := coord.GetJob(jobID); return err == nil && job.Status == coordinator.JobPaused })
	paused, err := coord.GetJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Load() != 1 || paused.PauseCheckpoint == 0 || paused.SavepointPath == "" {
		t.Fatalf("paused before durable teardown: %+v closed=%d", paused, closed.Load())
	}
	if err := coord.DeleteSavepoint(jobID, paused.PauseSavepointID); !errors.Is(err, coordinator.ErrSavepointInUse) {
		t.Fatalf("pause restore boundary could be deleted: %v", err)
	}
	response.Reset()
	if err := jobcli.Run(ctx, []string{"jobs", "resume", jobID, "--coordinator", url}, &response, &response); err != nil {
		t.Fatal(err)
	}
	select {
	case offset := <-restored:
		if offset != 1 {
			t.Fatalf("restored offset=%d", offset)
		}
	case <-ctx.Done():
		t.Fatal("resume never restored source")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("resumed job never completed")
	}
	events := sink.Events()
	if len(events) != 2 || string(events[0].Value) != "1" || string(events[1].Value) != "2" {
		t.Fatalf("restored keyed state/output: %+v", events)
	}
	if transactional {
		ledger.mu.Lock()
		visible := append([]string(nil), ledger.visible...)
		ledger.mu.Unlock()
		if fmt.Sprint(visible) != "[1 2]" {
			t.Fatalf("transactional output replayed or lost: %v", visible)
		}
	}
	finished, err := coord.GetJob(jobID)
	if err != nil || finished.RecoveryAttempts != 0 || finished.RestartCount != 0 {
		t.Fatalf("manual resume charged recovery: %+v %v", finished, err)
	}
}

func TestCLICancelWithSavepointPersistsBeforeStopping(t *testing.T) {
	release := make(chan struct{})
	var closed atomic.Int32
	registry := NewWorkerRegistry()
	registry.RegisterSource("replay", func(context.Context, []byte, WorkerTaskContext) (Source, error) {
		return &pauseReplaySource{release: release, restored: make(chan uint64, 1), closed: &closed}, nil
	})
	sink := &collectSink{}
	registry.RegisterSink("collect", func(context.Context, []byte, WorkerTaskContext) (Sink, error) { return sink, nil })
	ctx, coord, url := lifecycleCluster(t, registry)
	env := New().SetMode(Cluster).SetCoordinator(url)
	env.AddSourceNamed("source", "replay", nil).AddSinkNamed("sink", "collect", nil)
	done := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "savepoint-cancel"); done <- err }()
	var jobID string
	lifecycleWait(t, ctx, func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) != 1 || jobs[0].Status != coordinator.JobRunning || len(sink.Events()) != 1 {
			return false
		}
		jobID = jobs[0].ID
		return true
	})
	var response bytes.Buffer
	if err := jobcli.Run(ctx, []string{"jobs", "cancel", jobID, "--savepoint", "--coordinator", url}, &response, &response); err != nil {
		t.Fatal(err)
	}
	var accepted struct {
		Savepoint struct {
			ID string `json:"id"`
		} `json:"savepoint"`
	}
	if err := json.Unmarshal(response.Bytes(), &accepted); err != nil || accepted.Savepoint.ID == "" {
		t.Fatalf("missing accepted savepoint: %s %v", response.String(), err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation reported normal completion")
		}
	case <-ctx.Done():
		t.Fatal("savepoint cancellation did not finish")
	}
	job, err := coord.GetJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := coord.GetSavepoint(jobID, accepted.Savepoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != coordinator.JobCanceled || sp.Status != coordinator.SavepointCompleted || sp.CheckpointID == 0 || job.SavepointPath == "" || closed.Load() != 1 {
		t.Fatalf("canceled without completed savepoint/teardown: job=%+v savepoint=%+v closed=%d", job, sp, closed.Load())
	}
	if job.RecoveryAttempts != 0 || job.RestartCount != 0 {
		t.Fatalf("cancellation consumed recovery: %+v", job)
	}
}

func TestCLINodeRemovalRecoversOnRemainingWorker(t *testing.T) {
	release := make(chan struct{})
	var closed, starts atomic.Int32
	registry := NewWorkerRegistry()
	registry.RegisterSource("replay", func(context.Context, []byte, WorkerTaskContext) (Source, error) {
		if starts.Add(1) > 1 && closed.Load() == 0 {
			t.Error("new source started before old source stopped")
		}
		return &pauseReplaySource{release: release, restored: make(chan uint64, 2), closed: &closed}, nil
	})
	sink := &collectSink{}
	registry.RegisterSink("collect", func(context.Context, []byte, WorkerTaskContext) (Sink, error) { return sink, nil })
	ctx, coord, url := lifecycleCluster(t, registry, true)
	env := New().SetMode(Cluster).SetCoordinator(url).SetRestartStrategy(FixedDelay(3, 0))
	env.AddSourceNamed("source", "replay", nil).AddSinkNamed("sink", "collect", nil)
	done := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "remove-node"); done <- err }()
	var removed string
	lifecycleWait(t, ctx, func() bool {
		if len(sink.Events()) != 1 {
			return false
		}
		for _, w := range coord.ListWorkers() {
			if len(w.RunningTasks) != 0 {
				removed = w.ID
				return true
			}
		}
		return false
	})
	var response bytes.Buffer
	if err := jobcli.Run(ctx, []string{"cluster", "remove", removed, "--coordinator", url}, &response, &response); err != nil {
		t.Fatal(err)
	}
	lifecycleWait(t, ctx, func() bool { return starts.Load() == 2 && len(sink.Events()) >= 2 })
	for _, w := range coord.ListWorkers() {
		if w.ID == removed && !w.Removed {
			t.Fatal("removed worker regained admission")
		}
	}
	jobs := coord.ListJobs(nil)
	if len(jobs) != 1 || jobs[0].RestartCount != 1 {
		t.Fatalf("expected one recovery: %+v", jobs)
	}
	// The original source starts again from zero because no checkpoint preceded
	// removal. Keep the job unbounded until cancellation: with one replica worker
	// left, a final checkpoint would correctly have no independent replica.
	if _, err := coord.CancelJob(jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation reported normal completion")
		}
	case <-ctx.Done():
		t.Fatal("recovered job never stopped")
	}
}

func TestSavepointUpgradeRestoresAcrossJobIdentities(t *testing.T) { testSavepointUpgrade(t, false) }
func TestSavepointUpgradePreservesTransactionLineage(t *testing.T) { testSavepointUpgrade(t, true) }
func testSavepointUpgrade(t *testing.T, transactional bool) {
	release := make(chan struct{})
	restored := make(chan uint64, 2)
	var closed atomic.Int32
	registry := NewWorkerRegistry()
	registry.RegisterSource("replay", func(context.Context, []byte, WorkerTaskContext) (Source, error) {
		return &pauseReplaySource{release: release, restored: restored, closed: &closed}, nil
	})
	registry.RegisterKeyBy("key", func(context.Context, []byte, WorkerTaskContext) (KeySelector, error) {
		return func(e Event) ([]byte, error) { return e.Key, nil }, nil
	})
	for _, class := range []string{"count", "count-v2"} {
		registry.RegisterProcess(class, func(context.Context, []byte, WorkerTaskContext) (ProcessDefinition, error) {
			return ProcessDefinition{Process: func(ctx ProcessContext, e Event) ([]Event, error) {
				state := ctx.GetState("count")
				n, err := state.ValueInt64()
				if err != nil {
					return nil, err
				}
				if err := state.SetInt64(n + 1); err != nil {
					return nil, err
				}
				e.Value = []byte(fmt.Sprintf("%s:%d", class, n+1))
				return []Event{e}, nil
			}}, nil
		})
	}
	sink := &collectSink{}
	ledger := &pauseTransactionLedger{prepared: map[uint64][]string{}, committed: map[uint64]bool{}, loseResponse: true}
	registry.RegisterSink("collect", func(context.Context, []byte, WorkerTaskContext) (Sink, error) {
		if transactional {
			return &pauseTransactionSink{ledger: ledger, observed: sink}, nil
		}
		return sink, nil
	})
	ctx, coord, url := lifecycleCluster(t, registry)
	env := New().SetMode(Cluster).SetCoordinator(url)
	env.AddSourceNamed("source", "replay", nil).KeyByNamed("key", "key", nil).ProcessNamed("count", "count", nil).AddSinkNamed("sink", "collect", nil)
	done := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "upgrade-before"); done <- err }()
	var oldID string
	lifecycleWait(t, ctx, func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) != 1 || len(sink.Events()) != 1 {
			return false
		}
		oldID = jobs[0].ID
		return jobs[0].Status == coordinator.JobRunning
	})
	if _, _, err := coord.CancelJobWithSavepoint(oldID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("old job was not canceled")
		}
	case <-ctx.Done():
		t.Fatal("old job failed to stop")
	}
	old, err := coord.GetJob(oldID)
	if err != nil {
		t.Fatal(err)
	}
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(old.Config, &graph); err != nil {
		t.Fatal(err)
	}
	for i := range graph.Operators {
		if graph.Operators[i].ClassName == "count" {
			graph.Operators[i].ClassName = "count-v2"
		}
	}
	config, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		t.Fatal(err)
	}
	next, err := coord.SubmitJobFromSavepoint("upgrade-after", old.Parallelism, config, old.SavepointPath)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == old.ID {
		t.Fatal("upgrade reused runtime job ID")
	}
	select {
	case offset := <-restored:
		if offset != 1 {
			t.Fatalf("offset=%d", offset)
		}
	case <-ctx.Done():
		t.Fatal("upgrade did not restore")
	}
	if err := coord.DeleteSavepoint(old.ID, old.PauseSavepointID); !errors.Is(err, coordinator.ErrSavepointInUse) {
		t.Fatalf("source pin released too early: %v", err)
	}
	close(release)
	lifecycleWait(t, ctx, func() bool {
		job, err := coord.GetJob(next.ID)
		if err == nil && job.Status == coordinator.JobFailed {
			t.Fatalf("upgrade failed: %+v", job)
		}
		return err == nil && job.Status == coordinator.JobFinished
	})
	events := sink.Events()
	if len(events) != 2 || string(events[0].Value) != "count:1" || string(events[1].Value) != "count-v2:2" {
		t.Fatalf("upgrade offset/state/code: %+v", events)
	}
	if transactional {
		ledger.mu.Lock()
		visible := append([]string(nil), ledger.visible...)
		ledger.mu.Unlock()
		if fmt.Sprint(visible) != "[count:1 count-v2:2]" {
			t.Fatalf("transaction output replayed or lost: %v", visible)
		}
	}
	finished, err := coord.GetJob(next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.LatestCheckpoint <= old.LatestCheckpoint || finished.RestoreSavepoint != nil || finished.DeploymentGeneration <= old.DeploymentGeneration {
		t.Fatalf("upgrade did not advance fencing/boundary: %+v", finished)
	}
}
