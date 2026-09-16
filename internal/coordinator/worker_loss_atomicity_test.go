package coordinator

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type workerLossStore struct {
	MetadataStore
	failKey []byte
}

func (s *workerLossStore) Set(key []byte, value []byte) error {
	if bytes.Equal(key, s.failKey) {
		return errors.New("injected metadata outage")
	}
	return s.MetadataStore.Set(key, value)
}

func TestWorkerLossRetriesDurableTransitionAfterStorageFailure(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := slotReleaseJob(t, "job")
	job.Status = JobRunning
	c.jobs[job.ID] = job
	if err := store.Set(JobMetaKey(job.ID), encode(t, job)); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "old", Assignments: map[string]string{"task": "worker"}})); err != nil {
		t.Fatal(err)
	}
	c.workers["worker"] = &WorkerMeta{ID: "worker", LastHeartbeat: time.Now().Add(-2 * c.config.WorkerTimeout)}
	c.taskStatuses["task"] = rpc.TaskStatusRunning
	fault := &workerLossStore{MetadataStore: store, failKey: JobMetaKey(job.ID)}
	c.store = fault
	c.detectLostTaskWorkers()
	if job.Status != JobRunning {
		t.Fatal("failed metadata write published an undurable recovery state")
	}
	if !c.workers["worker"].Lost || c.taskStatuses["task"] != rpc.TaskStatusFailed {
		t.Fatal("storage outage restored expired execution authority")
	}
	fault.failKey = nil
	c.detectLostTaskWorkers()
	data, err := store.Get(JobMetaKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	var persisted JobMeta
	if err := protocol.DecodeMsgPack(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if job.Status != JobFailing || persisted.Status != JobFailing || c.jobs[job.ID] != job {
		t.Fatal("subsequent check did not publish durable failure on the same job")
	}
}

// Any metadata access on the timer or heartbeat rejection path is a regression:
// a slow store must not turn frequent liveness checks into global-lock I/O.
type noHealthIOStore struct{ MetadataStore }

func (*noHealthIOStore) Get([]byte) ([]byte, error) { panic("health check read metadata") }
func (*noHealthIOStore) Set([]byte, []byte) error   { panic("health check wrote metadata") }

func TestWorkerExpiryDefersAssignmentIOToScheduler(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := slotReleaseJob(t, "job")
	job.Status = JobRunning
	c.jobs[job.ID] = job
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, TaskAssignmentMap{JobID: job.ID, Assignments: map[string]string{"task": "worker"}})); err != nil {
		t.Fatal(err)
	}
	c.workers["worker"] = &WorkerMeta{ID: "worker", LastHeartbeat: time.Now().Add(-2 * c.config.WorkerTimeout), TaskSlotsAvailable: 1}
	c.taskStatuses["task"] = rpc.TaskStatusRunning
	c.store = &noHealthIOStore{store}
	if !c.expireTaskWorkers() {
		t.Fatal("expiry did not report newly lost worker")
	}
	for range 10 {
		if c.expireTaskWorkers() {
			t.Fatal("repeated expiry reported a new loss")
		}
	}
	value, err := c.HandleHeartbeat(context.Background(), 1, encode(t, rpc.HeartbeatRequest{WorkerID: "worker", EpochID: c.epoch}))
	if err != nil || value.(*rpc.HeartbeatResponse).Accepted {
		t.Fatalf("expired heartbeat accepted: %v %v", value, err)
	}
	if !c.workers["worker"].Lost || c.workers["worker"].TaskSlotsAvailable != 0 || job.Status != JobRunning || c.taskStatuses["task"] != rpc.TaskStatusRunning {
		t.Fatal("fast path did not defer recovery while fencing worker")
	}
	c.store = store
	c.detectLostTaskWorkers()
	if job.Status != JobFailing || c.taskStatuses["task"] != rpc.TaskStatusFailed {
		t.Fatal("scheduler did not recover expired worker's tasks")
	}
}
