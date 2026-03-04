package coordinator

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// newReadyCoordinator creates a coordinator that's in leader state with recovery complete.
func newReadyCoordinator(t *testing.T) (*Coordinator, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	t.Cleanup(func() { store.Close() })

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		HeartbeatFlushInterval: time.Hour,
	}, store, nil, zerolog.Nop())

	go c.Run(t.Context())
	// Wait for leader + recovery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.IsReady() {
			return c, store
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("coordinator did not become ready")
	return nil, nil
}

func TestSubmitJob_HappyPath(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, err := c.SubmitJob("my-job", 4, []byte("config: true"))
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if job.ID == "" {
		t.Fatal("expected non-empty job ID")
	}
	if job.Name != "my-job" {
		t.Fatalf("expected name my-job, got %s", job.Name)
	}
	if job.Parallelism != 4 {
		t.Fatalf("expected parallelism 4, got %d", job.Parallelism)
	}
	if job.Status != JobCreated {
		t.Fatalf("expected CREATED, got %s", job.Status)
	}
}

func TestSubmitJob_DuplicateName(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	_, err := c.SubmitJob("dup", 1, []byte("config"))
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}

	_, err = c.SubmitJob("dup", 1, []byte("config"))
	if !errors.Is(err, ErrJobExists) {
		t.Fatalf("expected ErrJobExists, got: %v", err)
	}
}

func TestSubmitJob_InvalidConfig(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	_, err := c.SubmitJob("", 1, nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for empty name, got: %v", err)
	}

	_, err = c.SubmitJob("valid", 0, nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for parallelism 0, got: %v", err)
	}
}

func TestGetJob(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	got, err := c.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Name != "j1" {
		t.Fatalf("expected j1, got %s", got.Name)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	_, err := c.GetJob("nonexistent")
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got: %v", err)
	}
}

func TestListJobs(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	c.SubmitJob("a", 1, []byte("cfg"))
	c.SubmitJob("b", 1, []byte("cfg"))

	all := c.ListJobs(nil)
	if len(all) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(all))
	}

	status := JobCreated
	filtered := c.ListJobs(&status)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 CREATED jobs, got %d", len(filtered))
	}

	status = JobRunning
	filtered = c.ListJobs(&status)
	if len(filtered) != 0 {
		t.Fatalf("expected 0 RUNNING jobs, got %d", len(filtered))
	}
}

func TestCancelJob(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	// Transition to a cancelable state: CREATED → DEPLOYING → RUNNING
	c.transitionJob(job, JobDeploying)
	c.transitionJob(job, JobRunning)

	canceled, err := c.CancelJob(job.ID)
	if err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
	if canceled.Status != JobCanceling {
		t.Fatalf("expected CANCELING, got %s", canceled.Status)
	}
}

func TestCancelJob_InvalidState(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	// Job is CREATED, which cannot transition to CANCELING.
	_, err := c.CancelJob(job.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got: %v", err)
	}
}

func TestPauseResumeJob(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	c.transitionJob(job, JobDeploying)
	c.transitionJob(job, JobRunning)

	paused, sp, err := c.PauseJob(job.ID)
	if err != nil {
		t.Fatalf("PauseJob: %v", err)
	}
	if paused.Status != JobPaused {
		t.Fatalf("expected PAUSED, got %s", paused.Status)
	}
	if sp == nil {
		t.Fatal("expected savepoint to be created")
	}

	resumed, err := c.ResumeJob(job.ID)
	if err != nil {
		t.Fatalf("ResumeJob: %v", err)
	}
	if resumed.Status != JobDeploying {
		t.Fatalf("expected DEPLOYING after resume, got %s", resumed.Status)
	}
}

func TestSubmitJob_ConcurrentDuplicateName(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	const goroutines = 20
	results := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			_, err := c.SubmitJob("same-name", 1, []byte("cfg"))
			results <- err
		}()
	}

	var successes, duplicates int
	for i := 0; i < goroutines; i++ {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrJobExists) {
			duplicates++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if successes != 1 {
		t.Fatalf("expected exactly 1 success, got %d", successes)
	}
	if duplicates != goroutines-1 {
		t.Fatalf("expected %d duplicates, got %d", goroutines-1, duplicates)
	}
}

func TestResumeJob_NotPaused(t *testing.T) {
	c, _ := newReadyCoordinator(t)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	c.transitionJob(job, JobDeploying)
	c.transitionJob(job, JobRunning)

	_, err := c.ResumeJob(job.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got: %v", err)
	}
}
