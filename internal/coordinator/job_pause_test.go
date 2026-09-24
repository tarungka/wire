package coordinator

import (
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestPauseIntentAndQueueAreAtomic(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	fault := &deploymentBatchStore{MetadataStore: store, fail: true}
	c.store = fault
	if _, _, err := c.PauseJob("job"); err == nil {
		t.Fatal("expected write failure")
	}
	points, err := c.ListSavepoints("job")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 0 || c.jobs["job"].PauseSavepointID != "" || c.queuedSavepointJobs["job"] {
		t.Fatal("failed request published partial intent")
	}
	fault.fail = false
	_, sp, err := c.PauseJob("job")
	if err != nil {
		t.Fatal(err)
	}
	_, retry, err := c.PauseJob("job")
	if err != nil || retry.ID != sp.ID {
		t.Fatal("duplicate pending pause created another snapshot")
	}
}

func pauseThroughBoundary(t *testing.T, c *Coordinator) *SavepointMeta {
	t.Helper()
	_, sp, err := c.PauseJob("job")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	completeQueueCheckpoint(t, c, c.activeCheckpoints["job"])
	if err := c.advancePause("job", time.Now().Add(c.config.WorkerTimeout)); err != nil {
		t.Fatal(err)
	}
	if c.jobs["job"].Status != JobPaused {
		t.Fatal("not paused")
	}
	return sp
}

func TestPauseResumeDoesNotFallBackToOlderBoundary(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	old, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	completeQueueCheckpoint(t, c, *old)
	pauseThroughBoundary(t, c)
	job := c.jobs["job"]
	raw, err := store.Get(CheckpointKey("job", job.PauseCheckpoint))
	if err != nil {
		t.Fatal(err)
	}
	var cp CheckpointMeta
	if err := protocol.DecodeMsgPack(raw, &cp); err != nil {
		t.Fatal(err)
	}
	cp.InvalidReason = "archive missing"
	if err := store.Set(CheckpointKey("job", cp.ID), encode(t, cp)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResumeJob("job"); err == nil {
		t.Fatal("resumed past the requested savepoint")
	}
	if job.Status != JobPaused || job.LatestCheckpoint != cp.ID {
		t.Fatal("failed resume changed paused state")
	}
}

func TestPauseFailureKeepsJobRunning(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	_, sp, err := c.PauseJob("job")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	cp := c.activeCheckpoints["job"]
	if err := c.AbortCheckpoint("job", cp.ID, cp.EpochID); err != nil {
		t.Fatal(err)
	}
	if err := c.advancePause("job", time.Now()); err != nil {
		t.Fatal(err)
	}
	job := c.jobs["job"]
	if job.Status != JobRunning || job.PauseSavepointID != "" || job.PauseFailure == "" {
		t.Fatalf("failed pause state: %+v", job)
	}
	if err := c.DeleteSavepoint("job", sp.ID); err != nil {
		t.Fatal("failed pause retained savepoint lock:", err)
	}
}

func TestPauseStagesSurviveCoordinatorRecovery(t *testing.T) {
	for _, stage := range []string{"queued", "pausing", "paused", "resuming"} {
		t.Run(stage, func(t *testing.T) {
			c, _ := checkpointPolicyCoordinator(t)
			_, sp, err := c.PauseJob("job")
			if err != nil {
				t.Fatal(err)
			}
			if stage != "queued" {
				if err := c.advanceQueuedSavepoint("job"); err != nil {
					t.Fatal(err)
				}
				completeQueueCheckpoint(t, c, c.activeCheckpoints["job"])
			}
			if stage == "paused" || stage == "resuming" {
				if err := c.advancePause("job", time.Now().Add(c.config.WorkerTimeout)); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "resuming" {
				if _, err := c.ResumeJob("job"); err != nil {
					t.Fatal(err)
				}
			}
			before := *c.jobs["job"]
			c.epoch++
			if err := c.recover(); err != nil {
				t.Fatal(err)
			}
			after := c.jobs["job"]
			if after.Status != before.Status || after.PauseCheckpoint != before.PauseCheckpoint || after.PauseSavepointID != sp.ID {
				t.Fatalf("lost pause stage on recovery: %+v", after)
			}
		})
	}
}

func TestPauseWithoutReplicaFailsWithoutStoppingJob(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	if err := store.Set(JobAssignmentsKey("job"), encode(t, TaskAssignmentMap{JobID: "job", Assignments: map[string]string{"task": "worker"}})); err != nil {
		t.Fatal(err)
	}
	_, sp, err := c.PauseJob("job")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.advanceQueuedSavepoint("job"); !errors.Is(err, ErrCheckpointUnavailable) {
		t.Fatalf("missing replica: %v", err)
	}
	if err := c.advancePause("job", time.Now()); err != nil {
		t.Fatal(err)
	}
	if c.jobs["job"].Status != JobRunning || c.jobs["job"].PauseFailure == "" {
		t.Fatal("invalid pause stopped the job or hid failure")
	}
	failed, err := c.GetSavepoint("job", sp.ID)
	if err != nil || failed.Status != SavepointFailed {
		t.Fatal("invalid pause remains queued")
	}
}

func TestResumeFailureDoesNotReleasePausedState(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	pauseThroughBoundary(t, c)
	fault := &workerLossStore{MetadataStore: store, failKey: JobMetaKey("job")}
	c.store = fault
	if _, err := c.ResumeJob("job"); err == nil {
		t.Fatal("expected persistence error")
	}
	if c.jobs["job"].Status != JobPaused {
		t.Fatal("undurable resume started execution")
	}
	fault.failKey = nil
	if _, err := c.ResumeJob("job"); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedPauseSnapshotReportsFailureAfterRecovery(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	if _, _, err := c.PauseJob("job"); err != nil {
		t.Fatal(err)
	}
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	c.epoch++
	if err := c.recover(); err != nil {
		t.Fatal(err)
	}
	if err := c.advancePause("job", time.Now()); err != nil {
		t.Fatal(err)
	}
	job := c.jobs["job"]
	if job.Status != JobRunning || job.PauseSavepointID != "" || job.PauseFailure == "" {
		t.Fatalf("interrupted pause remained pending or claimed completion: %+v", job)
	}
}
