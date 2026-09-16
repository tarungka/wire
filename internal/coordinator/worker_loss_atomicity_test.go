package coordinator

import (
	"bytes"
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
