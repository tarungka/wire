package coordinator

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestValidateTransition_Valid(t *testing.T) {
	tests := []struct {
		from, to JobStatus
	}{
		{JobCreated, JobDeploying},
		{JobDeploying, JobRunning},
		{JobDeploying, JobFailing},
		{JobRunning, JobFinishing},
		{JobRunning, JobPaused},
		{JobRunning, JobFailing},
		{JobRunning, JobCanceling},
		{JobPaused, JobDeploying},
		{JobFinishing, JobFinished},
		{JobFailing, JobDeploying},
		{JobFailing, JobFailed},
		{JobFailing, JobCanceled},
		{JobCanceling, JobCanceled},
	}

	for _, tt := range tests {
		t.Run(tt.from.String()+"→"+tt.to.String(), func(t *testing.T) {
			if err := ValidateTransition(tt.from, tt.to); err != nil {
				t.Fatalf("expected valid transition %s → %s, got error: %v", tt.from, tt.to, err)
			}
		})
	}
}

func TestValidateTransition_Invalid(t *testing.T) {
	tests := []struct {
		from, to JobStatus
	}{
		{JobCreated, JobRunning},
		{JobCreated, JobFinished},
		{JobRunning, JobCreated},
		{JobFinished, JobRunning},
		{JobFinished, JobDeploying},
		{JobFailed, JobRunning},
		{JobCanceled, JobRunning},
		{JobPaused, JobRunning},
		{JobPaused, JobCanceling},
	}

	for _, tt := range tests {
		t.Run(tt.from.String()+"→"+tt.to.String(), func(t *testing.T) {
			err := ValidateTransition(tt.from, tt.to)
			if err == nil {
				t.Fatalf("expected error for invalid transition %s → %s", tt.from, tt.to)
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got: %v", err)
			}
		})
	}
}

func TestTransitionJob_Timestamps(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())

	job := &JobMeta{
		ID:        "job-1",
		Name:      "test",
		Status:    JobCreated,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := c.persistJob(job); err != nil {
		t.Fatalf("persistJob: %v", err)
	}

	// CREATED → DEPLOYING
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transition to DEPLOYING: %v", err)
	}
	if !job.StartedAt.IsZero() {
		t.Fatal("StartedAt should not be set on DEPLOYING")
	}

	// DEPLOYING → RUNNING (first time sets StartedAt)
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transition to RUNNING: %v", err)
	}
	if job.StartedAt.IsZero() {
		t.Fatal("StartedAt should be set on first RUNNING")
	}
	startedAt := job.StartedAt

	// RUNNING → FAILING
	if err := c.transitionJob(job, JobFailing); err != nil {
		t.Fatalf("transition to FAILING: %v", err)
	}

	// FAILING → DEPLOYING (restart)
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transition to DEPLOYING (restart): %v", err)
	}
	if job.RestartCount != 1 {
		t.Fatalf("expected RestartCount=1, got %d", job.RestartCount)
	}

	// DEPLOYING → RUNNING again (StartedAt should not change)
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transition to RUNNING (2nd): %v", err)
	}
	if job.StartedAt != startedAt {
		t.Fatal("StartedAt should not change on subsequent RUNNING transitions")
	}

	// RUNNING → CANCELING → CANCELED (terminal sets FinishedAt)
	if err := c.transitionJob(job, JobCanceling); err != nil {
		t.Fatalf("transition to CANCELING: %v", err)
	}
	if !job.FinishedAt.IsZero() {
		t.Fatal("FinishedAt should not be set on CANCELING")
	}

	if err := c.transitionJob(job, JobCanceled); err != nil {
		t.Fatalf("transition to CANCELED: %v", err)
	}
	if job.FinishedAt.IsZero() {
		t.Fatal("FinishedAt should be set on terminal state")
	}
}

func TestTransitionJob_FailingToFailed(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())

	job := &JobMeta{
		ID:        "job-1",
		Name:      "test",
		Status:    JobCreated,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := c.persistJob(job); err != nil {
		t.Fatalf("persistJob: %v", err)
	}

	// CREATED → DEPLOYING → RUNNING → FAILING → FAILED
	for _, to := range []JobStatus{JobDeploying, JobRunning, JobFailing, JobFailed} {
		if err := c.transitionJob(job, to); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}

	if job.Status != JobFailed {
		t.Fatalf("expected FAILED, got %s", job.Status)
	}
	if job.FinishedAt.IsZero() {
		t.Fatal("FinishedAt should be set on FAILED (terminal)")
	}
}

func TestStateReachability_AllTerminalStatesReachable(t *testing.T) {
	// Verify that every terminal state (Finished, Failed, Canceled) can be
	// reached from JobCreated via valid transitions.
	reachable := map[JobStatus]bool{JobCreated: true}
	changed := true
	for changed {
		changed = false
		for from, targets := range validTransitions {
			if !reachable[from] {
				continue
			}
			for to := range targets {
				if !reachable[to] {
					reachable[to] = true
					changed = true
				}
			}
		}
	}

	terminals := []JobStatus{JobFinished, JobFailed, JobCanceled}
	for _, s := range terminals {
		if !reachable[s] {
			t.Errorf("terminal state %s is not reachable from CREATED", s)
		}
	}
}

func TestTransitionJob_InvalidReturnsError(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())

	job := &JobMeta{
		ID:        "job-1",
		Name:      "test",
		Status:    JobCreated,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := c.persistJob(job); err != nil {
		t.Fatalf("persistJob: %v", err)
	}

	err := c.transitionJob(job, JobRunning)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got: %v", err)
	}
	// Status should not change on invalid transition.
	if job.Status != JobCreated {
		t.Fatalf("expected status to remain CREATED, got %s", job.Status)
	}
}
