package coordinator

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

func TestRecovery_EmptyStore(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if len(state.jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(state.jobs))
	}
	if len(state.workers) != 0 {
		t.Fatalf("expected 0 workers, got %d", len(state.workers))
	}
	if state.epoch != 1 {
		t.Fatalf("expected epoch 1 (0+1), got %d", state.epoch)
	}
}

func TestRecovery_WithJobs(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Persist some jobs.
	jobs := []*JobMeta{
		{ID: "j1", Name: "job-1", Status: JobRunning, Parallelism: 2},
		{ID: "j2", Name: "job-2", Status: JobFinished, Parallelism: 4},
		{ID: "j3", Name: "job-3", Status: JobFailed, Parallelism: 1},
	}
	for _, j := range jobs {
		data, err := protocol.EncodeMsgPack(j)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Set(JobMetaKey(j.ID), data); err != nil {
			t.Fatalf("store.Set: %v", err)
		}
	}

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if len(state.jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(state.jobs))
	}

	j1 := state.jobs["j1"]
	if j1.Status != JobRunning {
		t.Fatalf("j1 status: expected RUNNING, got %s", j1.Status)
	}
	j2 := state.jobs["j2"]
	if j2.Status != JobFinished {
		t.Fatalf("j2 status: expected FINISHED, got %s", j2.Status)
	}
}

func TestRecovery_WithWorkers(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	worker := &WorkerMeta{
		ID:             "w1",
		Address:        "localhost:5001",
		TaskSlotsTotal: 4,
		LastHeartbeat:  time.Now().UTC(),
	}
	data, err := protocol.EncodeMsgPack(worker)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(WorkerMetaKey("w1"), data); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if len(state.workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(state.workers))
	}
	if _, ok := state.workers["w1"]; !ok {
		t.Fatal("w1 not recovered")
	}
}

func TestRecovery_WorkerHeartbeatZeroed(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	worker := &WorkerMeta{
		ID:             "w1",
		Address:        "localhost:5001",
		TaskSlotsTotal: 4,
		LastHeartbeat:  time.Now().UTC(),
	}
	data, err := protocol.EncodeMsgPack(worker)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(WorkerMetaKey("w1"), data); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}

	w := state.workers["w1"]
	if !w.LastHeartbeat.IsZero() {
		t.Fatalf("expected zeroed heartbeat after recovery, got %v", w.LastHeartbeat)
	}
}

func TestRecovery_CorruptConfigFails(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	if err := store.Set(ClusterConfigKey(), []byte("corrupt-config-data")); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	_, err := recoverFromStore(store)
	if err == nil {
		t.Fatal("expected error for corrupt config")
	}
	if !errors.Is(err, ErrStoreCorrupted) {
		t.Fatalf("expected ErrStoreCorrupted, got: %v", err)
	}
}

func TestRecovery_RoundTrip(t *testing.T) {
	// Verify that a coordinator can persist state, then a fresh coordinator
	// can recover it correctly.
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		HeartbeatFlushInterval: time.Hour,
	}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.recovered = true
	c.mu.Unlock()

	// Create jobs.
	j1, err := c.SubmitJob("job-alpha", 2, []byte("config-a"))
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if err := c.transitionJob(j1, JobDeploying); err != nil {
		t.Fatalf("transitionJob to Deploying: %v", err)
	}
	if err := c.transitionJob(j1, JobRunning); err != nil {
		t.Fatalf("transitionJob to Running: %v", err)
	}

	j2, err := c.SubmitJob("job-beta", 4, []byte("config-b"))
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	// Add a worker.
	if err := c.persistWorker(&WorkerMeta{
		ID:             "w1",
		Address:        "localhost:5001",
		TaskSlotsTotal: 8,
		LastHeartbeat:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persistWorker: %v", err)
	}

	// Recover from store.
	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}

	if len(state.jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(state.jobs))
	}
	if state.jobs[j1.ID].Status != JobRunning {
		t.Fatalf("j1 status: expected RUNNING, got %s", state.jobs[j1.ID].Status)
	}
	if state.jobs[j2.ID].Status != JobCreated {
		t.Fatalf("j2 status: expected CREATED, got %s", state.jobs[j2.ID].Status)
	}

	if len(state.workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(state.workers))
	}
	w := state.workers["w1"]
	if !w.LastHeartbeat.IsZero() {
		t.Fatal("recovered worker heartbeat should be zeroed")
	}
	if w.TaskSlotsTotal != 8 {
		t.Fatalf("expected 8 task slots, got %d", w.TaskSlotsTotal)
	}
}

func TestRecovery_InFlightCheckpointAbort(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Persist a job.
	job := &JobMeta{ID: "j1", Name: "job-1", Status: JobRunning}
	jobData, err := protocol.EncodeMsgPack(job)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(JobMetaKey("j1"), jobData); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Persist a completed checkpoint.
	cpCompleted := &CheckpointMeta{
		ID:     1,
		JobID:  "j1",
		Status: CheckpointCompleted,
	}
	cpData, err := protocol.EncodeMsgPack(cpCompleted)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(CheckpointKey("j1", 1), cpData); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Persist an in-flight checkpoint.
	cpInFlight := &CheckpointMeta{
		ID:     2,
		JobID:  "j1",
		Status: CheckpointTriggered,
	}
	cpData2, err := protocol.EncodeMsgPack(cpInFlight)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(CheckpointKey("j1", 2), cpData2); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}

	// Latest completed should be checkpoint 1.
	latest, ok := state.latestCheckpoints["j1"]
	if !ok {
		t.Fatal("no latest checkpoint for j1")
	}
	if latest.ID != 1 {
		t.Fatalf("expected latest checkpoint ID 1, got %d", latest.ID)
	}

	// In-flight checkpoint should be marked for abort.
	if len(state.checkpointsToAbort) != 1 {
		t.Fatalf("expected 1 checkpoint to abort, got %d", len(state.checkpointsToAbort))
	}
	if state.checkpointsToAbort[0].ID != 2 {
		t.Fatalf("expected aborted checkpoint ID 2, got %d", state.checkpointsToAbort[0].ID)
	}
	if state.checkpointsToAbort[0].Status != CheckpointAborted {
		t.Fatalf("expected ABORTED status, got %s", state.checkpointsToAbort[0].Status)
	}
}

func TestRecovery_InFlightSavepointMarkedFailed(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Persist a job.
	job := &JobMeta{ID: "j1", Name: "job-1", Status: JobRunning}
	jobData, err := protocol.EncodeMsgPack(job)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(JobMetaKey("j1"), jobData); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Persist an in-progress savepoint.
	sp := &SavepointMeta{
		ID:     "sp-abc",
		JobID:  "j1",
		Status: SavepointInProgress,
	}
	spData, err := protocol.EncodeMsgPack(sp)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(SavepointKey("j1", "sp-abc"), spData); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}

	if len(state.savepointsToFail) != 1 {
		t.Fatalf("expected 1 savepoint to fail, got %d", len(state.savepointsToFail))
	}
	if state.savepointsToFail[0].ID != "sp-abc" {
		t.Fatalf("expected savepoint sp-abc, got %s", state.savepointsToFail[0].ID)
	}
	if state.savepointsToFail[0].Status != SavepointFailed {
		t.Fatalf("expected FAILED status, got %s", state.savepointsToFail[0].Status)
	}
}

func TestRecovery_EpochIncrement(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	if err := c.persistEpoch(10); err != nil {
		t.Fatalf("persistEpoch: %v", err)
	}

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if state.epoch != 11 {
		t.Fatalf("expected epoch 11, got %d", state.epoch)
	}

	// Verify persisted epoch.
	storedEpoch, err := store.Get(ClusterEpochKey())
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if storedEpoch == nil {
		t.Fatal("epoch not persisted")
	}
}

func TestRecovery_AllJobStates(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	allStatuses := []JobStatus{
		JobCreated, JobDeploying, JobRunning, JobFinishing, JobFinished,
		JobFailing, JobFailed, JobCanceling, JobCanceled,
	}

	for i, s := range allStatuses {
		job := &JobMeta{
			ID:     s.String(),
			Name:   s.String(),
			Status: s,
		}
		data, err := protocol.EncodeMsgPack(job)
		if err != nil {
			t.Fatalf("EncodeMsgPack[%d]: %v", i, err)
		}
		if err := store.Set(JobMetaKey(job.ID), data); err != nil {
			t.Fatalf("store.Set[%d]: %v", i, err)
		}
	}

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if len(state.jobs) != len(allStatuses) {
		t.Fatalf("expected %d jobs, got %d", len(allStatuses), len(state.jobs))
	}
}

func TestRecovery_CorruptJobFailsFast(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Write a valid job.
	job := &JobMeta{ID: "j1", Name: "good-job", Status: JobRunning}
	data, err := protocol.EncodeMsgPack(job)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(JobMetaKey("j1"), data); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Write a corrupt job entry (invalid msgpack).
	if err := store.Set(JobMetaKey("j2"), []byte("corrupt-data-not-msgpack")); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	_, err = recoverFromStore(store)
	if err == nil {
		t.Fatal("expected error for corrupt job entry")
	}
	if !errors.Is(err, ErrStoreCorrupted) {
		t.Fatalf("expected ErrStoreCorrupted, got: %v", err)
	}
}

func TestRecovery_CorruptWorkerFailsFast(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Write a corrupt worker entry.
	if err := store.Set(WorkerMetaKey("w1"), []byte("corrupt-worker")); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	_, err := recoverFromStore(store)
	if err == nil {
		t.Fatal("expected error for corrupt worker entry")
	}
	if !errors.Is(err, ErrStoreCorrupted) {
		t.Fatalf("expected ErrStoreCorrupted, got: %v", err)
	}
}

func TestRecovery_CorruptCheckpointFailsFast(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Need a valid job first (checkpoints are scanned per-job).
	job := &JobMeta{ID: "j1", Name: "job-1", Status: JobRunning}
	data, err := protocol.EncodeMsgPack(job)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(JobMetaKey("j1"), data); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Write a corrupt checkpoint.
	if err := store.Set(CheckpointKey("j1", 1), []byte("corrupt-checkpoint")); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	_, err = recoverFromStore(store)
	if err == nil {
		t.Fatal("expected error for corrupt checkpoint entry")
	}
	if !errors.Is(err, ErrStoreCorrupted) {
		t.Fatalf("expected ErrStoreCorrupted, got: %v", err)
	}
}

func TestRecovery_CheckpointsToAbortPersisted(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Persist a job.
	job := &JobMeta{ID: "j1", Name: "job-1", Status: JobRunning}
	jobData, err := protocol.EncodeMsgPack(job)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(JobMetaKey("j1"), jobData); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Persist an in-flight checkpoint (Triggered).
	cpTriggered := &CheckpointMeta{
		ID:     1,
		JobID:  "j1",
		Status: CheckpointTriggered,
	}
	cpData, err := protocol.EncodeMsgPack(cpTriggered)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(CheckpointKey("j1", 1), cpData); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Persist an in-progress checkpoint.
	cpInProgress := &CheckpointMeta{
		ID:     2,
		JobID:  "j1",
		Status: CheckpointInProgress,
	}
	cpData2, err := protocol.EncodeMsgPack(cpInProgress)
	if err != nil {
		t.Fatalf("EncodeMsgPack: %v", err)
	}
	if err := store.Set(CheckpointKey("j1", 2), cpData2); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Run coordinator recovery (not just recoverFromStore).
	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.mu.Unlock()

	if err := c.recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}

	// Verify both checkpoints were persisted as ABORTED in the store.
	for _, cpID := range []uint64{1, 2} {
		data, err := store.Get(CheckpointKey("j1", cpID))
		if err != nil {
			t.Fatalf("store.Get checkpoint %d: %v", cpID, err)
		}
		if data == nil {
			t.Fatalf("checkpoint %d not in store", cpID)
		}
		var cp CheckpointMeta
		if err := protocol.DecodeMsgPack(data, &cp); err != nil {
			t.Fatalf("decode checkpoint %d: %v", cpID, err)
		}
		if cp.Status != CheckpointAborted {
			t.Fatalf("checkpoint %d: expected ABORTED, got %s", cpID, cp.Status)
		}
	}
}
