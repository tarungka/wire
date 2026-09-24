package coordinator

import (
	"errors"
	"testing"
)

func TestDeleteSavepointProtectsLatestActiveRecoveryBoundary(t *testing.T) {
	for _, status := range []JobStatus{JobCreated, JobDeploying, JobRunning, JobFinishing, JobFailing, JobCanceling, JobPausing, JobPaused, JobResuming} {
		t.Run(status.String(), func(t *testing.T) {
			c, _ := newReadyCoordinator(t)
			if err := c.store.Set(CheckpointKey("job", 7), encode(t, CheckpointMeta{JobID: "job", ID: 7, SavepointID: "save", Status: CheckpointCompleted})); err != nil {
				t.Fatal(err)
			}
			c.jobs["job"] = &JobMeta{ID: "job", Status: status, LatestCheckpoint: 7}
			sp := &SavepointMeta{ID: "save", JobID: "job", Status: SavepointCompleted, CheckpointID: 7}
			if err := c.persistSavepoint(sp); err != nil {
				t.Fatal(err)
			}
			if err := c.DeleteSavepoint("job", "save"); !errors.Is(err, ErrSavepointInUse) {
				t.Fatalf("latest boundary deletion: %v", err)
			}
			if _, err := c.GetSavepoint("job", "save"); err != nil {
				t.Fatal("protected metadata removed", err)
			}
			c.jobs["job"].LatestCheckpoint = 8
			if err := c.DeleteSavepoint("job", "save"); err != nil {
				t.Fatal("newer boundary did not release pin", err)
			}
		})
	}
	for _, status := range []JobStatus{JobFinished, JobFailed, JobCanceled} {
		t.Run(status.String(), func(t *testing.T) {
			c, _ := newReadyCoordinator(t)
			if err := c.store.Set(CheckpointKey("job", 7), encode(t, CheckpointMeta{JobID: "job", ID: 7, SavepointID: "save", Status: CheckpointCompleted})); err != nil {
				t.Fatal(err)
			}
			c.jobs["job"] = &JobMeta{ID: "job", Status: status, LatestCheckpoint: 7}
			if err := c.persistSavepoint(&SavepointMeta{ID: "save", JobID: "job", Status: SavepointCompleted, CheckpointID: 7}); err != nil {
				t.Fatal(err)
			}
			if err := c.DeleteSavepoint("job", "save"); err != nil {
				t.Fatal("terminal job retained recovery pin", err)
			}
		})
	}
}
