package coordinator

import (
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestSavepointFollowsDurableCheckpointDecision(t *testing.T) {
	for _, abort := range []bool{false, true} {
		c, store := newTestCoordinator(t)
		c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
		if err := store.Set(JobAssignmentsKey("job"), encode(t, TaskAssignmentMap{TaskDescriptors: manifestTaskDescriptors("a", "b"), JobID: "job", Assignments: map[string]string{"a": "w1", "b": "w2"}, Replicas: map[string]string{"a": "replica/a", "b": "replica/b"}})); err != nil {
			t.Fatal(err)
		}
		sp, err := c.TriggerSavepoint("job")
		if err != nil {
			t.Fatal(err)
		}
		if sp.CheckpointID == 0 || sp.EpochID != 5 || sp.Status != SavepointInProgress {
			t.Fatalf("boundary=%+v", sp)
		}
		request := rpc.AcknowledgeCheckpointRequest{JobID: "job", TaskID: "a", WorkerID: "w1", CheckpointID: sp.CheckpointID, EpochID: sp.EpochID, State: manifestState(t, "a", "replica/a")}
		if err := c.AcknowledgeCheckpoint(request); err != nil {
			t.Fatal(err)
		}
		partial, err := c.GetSavepoint("job", sp.ID)
		if err != nil || partial.Status != SavepointInProgress {
			t.Fatalf("early completion: %+v %v", partial, err)
		}
		want := SavepointCompleted
		if abort {
			want = SavepointFailed
			err = c.AbortCheckpoint("job", sp.CheckpointID, sp.EpochID)
		} else {
			request.TaskID, request.WorkerID, request.State = "b", "w2", manifestState(t, "b", "replica/b")
			err = c.AcknowledgeCheckpoint(request)
		}
		if err != nil {
			t.Fatal(err)
		}
		final, err := c.GetSavepoint("job", sp.ID)
		if err != nil || final.Status != want || final.CompletionTime.IsZero() {
			t.Fatalf("decision=%+v %v", final, err)
		}
	}
}

func installSavepointAssignment(t *testing.T, c *Coordinator, jobID string) {
	t.Helper()
	if err := c.store.Set(JobAssignmentsKey(jobID), encode(t, TaskAssignmentMap{JobID: jobID, Assignments: map[string]string{"task": "worker"}, Replicas: map[string]string{"task": "replica"}})); err != nil {
		t.Fatal(err)
	}
}

func abortPendingSavepoint(t *testing.T, c *Coordinator, jobID string) {
	t.Helper()
	points, err := c.ListSavepoints(jobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range points {
		if sp.Status == SavepointInProgress {
			if err := c.AbortCheckpoint(jobID, sp.CheckpointID, sp.EpochID); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDeleteSavepointWaitsForDecisionAndAllowsAbortRetry(t *testing.T) {
	c, _ := newTestCoordinator(t)
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
	installSavepointAssignment(t, c, "job")
	sp, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSavepoint("job", sp.ID); err != ErrCheckpointInProgress {
		t.Fatalf("active deletion: %v", err)
	}
	if err := c.AbortCheckpoint("job", sp.CheckpointID, sp.EpochID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSavepoint("job", sp.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.AbortCheckpoint("job", sp.CheckpointID, sp.EpochID); err != nil {
		t.Fatalf("abort retry after deletion: %v", err)
	}
}

func TestRescaleSavepointRetainedUntilReplacementCheckpoint(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := &JobMeta{ID: "job", Status: JobFailing, RescaleCheckpoint: 7, LatestCheckpoint: 7}
	c.jobs["job"] = job
	sp := SavepointMeta{ID: "save", JobID: "job", CheckpointID: 7, Status: SavepointCompleted}
	if err := store.Set(SavepointKey("job", "save"), encode(t, sp)); err != nil {
		t.Fatal(err)
	}
	for _, status := range []JobStatus{JobFailing, JobDeploying, JobRunning} {
		job.Status = status
		if err := c.DeleteSavepoint("job", "save"); err != ErrSavepointInUse {
			t.Fatalf("status %v deletion: %v", status, err)
		}
		if _, err := c.GetSavepoint("job", "save"); err != nil {
			t.Fatal(err)
		}
	}
	job.LatestCheckpoint = 8
	if err := c.DeleteSavepoint("job", "save"); err != nil {
		t.Fatal(err)
	}
}
