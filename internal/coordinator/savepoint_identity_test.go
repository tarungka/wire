package coordinator

import (
	"errors"
	"sync"
	"testing"
)

func TestIdentifiedSavepointIsDurableAndDeduplicated(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	if err := store.Set(JobMetaKey("job"), encode(t, c.jobs["job"])); err != nil {
		t.Fatal(err)
	}
	id := generateSavepointID()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			sp, err := c.queueIdentifiedSavepoint("job", id)
			if err != nil || sp == nil || sp.ID != id {
				t.Errorf("request=%+v error=%v", sp, err)
			}
		})
	}
	wg.Wait()
	points, err := c.ListSavepoints("job")
	if err != nil || len(points) != 1 || !points[0].Queued {
		t.Fatalf("queue=%+v error=%v", points, err)
	}
	c.epoch++
	if err := c.recover(); err != nil {
		t.Fatal(err)
	}
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	active := c.activeCheckpoints["job"]
	repeated, err := c.queueIdentifiedSavepoint("job", id)
	if err != nil || repeated.CheckpointID != active.ID || repeated.Queued {
		t.Fatalf("active request overwritten: %+v %v", repeated, err)
	}
	completeQueueCheckpoint(t, c, active)
	completed, err := c.queueIdentifiedSavepoint("job", id)
	if err != nil || completed.Status != SavepointCompleted || completed.CheckpointID != active.ID {
		t.Fatalf("completed request overwritten: %+v %v", completed, err)
	}
	if len(c.activeCheckpoints) != 0 {
		t.Fatal("duplicate request started another checkpoint")
	}
}

func TestIdentifiedSavepointCannotReuseDeletedID(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	id := generateSavepointID()
	if _, err := c.queueIdentifiedSavepoint("job", id); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSavepoint("job", id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.queueIdentifiedSavepoint("job", id); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("deleted ID reused: %v", err)
	}
	for _, bad := range []string{"", "../job", "sp-ABCDEF00000000000000000000000000", "sp-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"} {
		if _, err := c.queueIdentifiedSavepoint("job", bad); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("invalid ID accepted %q: %v", bad, err)
		}
	}
}

func TestIdentifiedSavepointFailedPersistenceDoesNotPublish(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	id := generateSavepointID()
	c.store = &workerLossStore{MetadataStore: store, failKey: SavepointKey("job", id)}
	if _, err := c.queueIdentifiedSavepoint("job", id); err == nil {
		t.Fatal("failed persistence reported acceptance")
	}
	if c.queuedSavepointJobs["job"] {
		t.Fatal("failed request published to queue")
	}
	if _, err := c.GetSavepoint("job", id); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("request persisted: %v", err)
	}
	c.store = store
	if _, err := c.queueIdentifiedSavepoint("job", id); err != nil {
		t.Fatal(err)
	}
}
