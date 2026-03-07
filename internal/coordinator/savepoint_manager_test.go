package coordinator

import (
	"errors"
	"testing"
)

func TestTriggerSavepoint(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transitionJob to DEPLOYING: %v", err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transitionJob to RUNNING: %v", err)
	}

	sp, err := c.TriggerSavepoint(job.ID)
	if err != nil {
		t.Fatalf("TriggerSavepoint: %v", err)
	}
	if sp.ID == "" {
		t.Fatal("expected non-empty savepoint ID")
	}
	if sp.JobID != job.ID {
		t.Fatalf("expected job ID %s, got %s", job.ID, sp.JobID)
	}
	if sp.Status != SavepointInProgress {
		t.Fatalf("expected IN_PROGRESS, got %s", sp.Status)
	}
}

func TestTriggerSavepoint_NotRunning(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	// Job is CREATED, not RUNNING.
	_, err := c.TriggerSavepoint(job.ID)
	if !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("expected ErrJobNotRunning, got: %v", err)
	}
}

func TestGetSavepoint(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transitionJob to DEPLOYING: %v", err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transitionJob to RUNNING: %v", err)
	}

	sp, err := c.TriggerSavepoint(job.ID)
	if err != nil {
		t.Fatalf("TriggerSavepoint: %v", err)
	}

	got, err := c.GetSavepoint(job.ID, sp.ID)
	if err != nil {
		t.Fatalf("GetSavepoint: %v", err)
	}
	if got.ID != sp.ID {
		t.Fatalf("expected savepoint %s, got %s", sp.ID, got.ID)
	}
}

func TestGetSavepoint_NotFound(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	_, err := c.GetSavepoint("job-1", "sp-nonexistent")
	if !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("expected ErrSavepointNotFound, got: %v", err)
	}
}

func TestListSavepoints(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transitionJob to DEPLOYING: %v", err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transitionJob to RUNNING: %v", err)
	}

	if _, err := c.TriggerSavepoint(job.ID); err != nil {
		t.Fatalf("TriggerSavepoint 1: %v", err)
	}
	if _, err := c.TriggerSavepoint(job.ID); err != nil {
		t.Fatalf("TriggerSavepoint 2: %v", err)
	}

	sps, err := c.ListSavepoints(job.ID)
	if err != nil {
		t.Fatalf("ListSavepoints: %v", err)
	}
	if len(sps) != 2 {
		t.Fatalf("expected 2 savepoints, got %d", len(sps))
	}
}

func TestDeleteSavepoint(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transitionJob to DEPLOYING: %v", err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transitionJob to RUNNING: %v", err)
	}

	sp, err := c.TriggerSavepoint(job.ID)
	if err != nil {
		t.Fatalf("TriggerSavepoint: %v", err)
	}

	if err := c.DeleteSavepoint(job.ID, sp.ID); err != nil {
		t.Fatalf("DeleteSavepoint: %v", err)
	}

	// Should be gone now.
	_, err = c.GetSavepoint(job.ID, sp.ID)
	if !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("expected ErrSavepointNotFound after delete, got: %v", err)
	}
}

func TestDeleteSavepoint_NotFound(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	err := c.DeleteSavepoint("job-1", "sp-nope")
	if !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("expected ErrSavepointNotFound, got: %v", err)
	}
}
