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
	defer store.Close()

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
	defer store.Close()

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
		store.Set(JobMetaKey(j.ID), data)
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
	defer store.Close()

	worker := &WorkerMeta{
		ID:             "w1",
		Address:        "localhost:5001",
		TaskSlotsTotal: 4,
		LastHeartbeat:  time.Now().UTC(),
	}
	data, _ := protocol.EncodeMsgPack(worker)
	store.Set(WorkerMetaKey("w1"), data)

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

func TestRecovery_InFlightCheckpointAbort(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	// Persist a job.
	job := &JobMeta{ID: "j1", Name: "job-1", Status: JobRunning}
	jobData, _ := protocol.EncodeMsgPack(job)
	store.Set(JobMetaKey("j1"), jobData)

	// Persist a completed checkpoint.
	cpCompleted := &CheckpointMeta{
		ID:     1,
		JobID:  "j1",
		Status: CheckpointCompleted,
	}
	cpData, _ := protocol.EncodeMsgPack(cpCompleted)
	store.Set(CheckpointKey("j1", 1), cpData)

	// Persist an in-flight checkpoint.
	cpInFlight := &CheckpointMeta{
		ID:     2,
		JobID:  "j1",
		Status: CheckpointTriggered,
	}
	cpData2, _ := protocol.EncodeMsgPack(cpInFlight)
	store.Set(CheckpointKey("j1", 2), cpData2)

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

func TestRecovery_EpochIncrement(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.persistEpoch(10)

	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if state.epoch != 11 {
		t.Fatalf("expected epoch 11, got %d", state.epoch)
	}

	// Verify persisted epoch.
	storedEpoch, _ := store.Get(ClusterEpochKey())
	if storedEpoch == nil {
		t.Fatal("epoch not persisted")
	}
}

func TestRecovery_AllJobStates(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

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
		data, _ := protocol.EncodeMsgPack(job)
		store.Set(JobMetaKey(job.ID), data)
		_ = i
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
	defer store.Close()

	// Write a valid job.
	job := &JobMeta{ID: "j1", Name: "good-job", Status: JobRunning}
	data, _ := protocol.EncodeMsgPack(job)
	store.Set(JobMetaKey("j1"), data)

	// Write a corrupt job entry (invalid msgpack).
	store.Set(JobMetaKey("j2"), []byte("corrupt-data-not-msgpack"))

	_, err := recoverFromStore(store)
	if err == nil {
		t.Fatal("expected error for corrupt job entry")
	}
	if !errors.Is(err, ErrStoreCorrupted) {
		t.Fatalf("expected ErrStoreCorrupted, got: %v", err)
	}
}

func TestRecovery_CorruptWorkerFailsFast(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	// Write a corrupt worker entry.
	store.Set(WorkerMetaKey("w1"), []byte("corrupt-worker"))

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
	defer store.Close()

	// Need a valid job first (checkpoints are scanned per-job).
	job := &JobMeta{ID: "j1", Name: "job-1", Status: JobRunning}
	data, _ := protocol.EncodeMsgPack(job)
	store.Set(JobMetaKey("j1"), data)

	// Write a corrupt checkpoint.
	store.Set(CheckpointKey("j1", 1), []byte("corrupt-checkpoint"))

	_, err := recoverFromStore(store)
	if err == nil {
		t.Fatal("expected error for corrupt checkpoint entry")
	}
	if !errors.Is(err, ErrStoreCorrupted) {
		t.Fatalf("expected ErrStoreCorrupted, got: %v", err)
	}
}

func TestRecovery_CheckpointsToAbortPersisted(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	// Persist a job.
	job := &JobMeta{ID: "j1", Name: "job-1", Status: JobRunning}
	jobData, _ := protocol.EncodeMsgPack(job)
	store.Set(JobMetaKey("j1"), jobData)

	// Persist an in-flight checkpoint (Triggered).
	cpTriggered := &CheckpointMeta{
		ID:     1,
		JobID:  "j1",
		Status: CheckpointTriggered,
	}
	cpData, _ := protocol.EncodeMsgPack(cpTriggered)
	store.Set(CheckpointKey("j1", 1), cpData)

	// Persist an in-progress checkpoint.
	cpInProgress := &CheckpointMeta{
		ID:     2,
		JobID:  "j1",
		Status: CheckpointInProgress,
	}
	cpData2, _ := protocol.EncodeMsgPack(cpInProgress)
	store.Set(CheckpointKey("j1", 2), cpData2)

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
