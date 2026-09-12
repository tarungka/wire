package coordinator

import (
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

type deploymentBatchStore struct {
	MetadataStore
	fail    bool
	batches int
}

func (s *deploymentBatchStore) WriteBatch(batch []KVPair) error {
	s.batches++
	if s.fail {
		return errors.New("injected batch failure")
	}
	return s.MetadataStore.WriteBatch(batch)
}

func TestScheduleJob_AtomicDeployment(t *testing.T) {
	base := NewMemoryStore()
	defer func() { _ = base.Close() }()
	store := &deploymentBatchStore{MetadataStore: base, fail: true}
	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.state = StateLeader
	job := &JobMeta{ID: "j1", Name: "job", Status: JobCreated, Parallelism: 1, Config: encode(t, linearGraph())}
	if err := c.persistJob(job); err != nil {
		t.Fatal(err)
	}
	c.workers["w1"] = &WorkerMeta{ID: "w1", TaskSlotsTotal: 1, TaskSlotsAvailable: 1}
	c.scheduleJob(job)
	if job.Status != JobCreated || c.workers["w1"].TaskSlotsAvailable != 1 || len(c.DrainCommands("w1")) != 0 {
		t.Fatal("failed persistence published deployment or consumed worker slot")
	}
	data, err := base.Get(JobAssignmentsKey("j1"))
	if err != nil || data != nil {
		t.Fatalf("partial assignments: %q, %v", data, err)
	}
	recovered, err := recoverFromStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.jobs["j1"].Status != JobCreated {
		t.Fatal("failed deployment changed durable job")
	}
	store.fail = false
	c.scheduleJob(job)
	if store.batches != 2 || job.Status != JobDeploying || c.workers["w1"].TaskSlotsAvailable != 0 || len(c.DrainCommands("w1")) != 1 {
		t.Fatal("retry did not atomically persist and deploy exactly once")
	}
	recovered, err = recoverFromStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.jobs["j1"].Status != JobDeploying {
		t.Fatal("deployment not durable")
	}
	data, err = base.Get(JobAssignmentsKey("j1"))
	if err != nil {
		t.Fatal(err)
	}
	var assignments TaskAssignmentMap
	if err := protocol.DecodeMsgPack(data, &assignments); err != nil {
		t.Fatal(err)
	}
	if len(assignments.Assignments) != 1 {
		t.Fatalf("assignments: %+v", assignments)
	}
}
