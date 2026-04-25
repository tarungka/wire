package coordinator

import (
	"fmt"
	"time"
)

// validTransitions defines the set of legal job state transitions.
//
// JobCreated -> JobFailing is allowed for permanent pre-deployment failures
// (e.g. malformed graph, missing operator factory) so the scheduler can move
// such a job out of the queue immediately rather than retrying forever.
var validTransitions = map[JobStatus][]JobStatus{
	JobCreated:   {JobDeploying, JobFailing, JobCanceling},
	JobDeploying: {JobRunning, JobFailing, JobCanceling},
	JobRunning:   {JobFinishing, JobPaused, JobFailing, JobCanceling},
	JobPaused:    {JobDeploying},
	JobFinishing: {JobFinished},
	JobFailing:   {JobDeploying, JobFailed, JobCanceled},
	JobCanceling: {JobCanceled},
	// Terminal states have no outgoing transitions.
	JobFinished: {},
	JobFailed:   {},
	JobCanceled: {},
}

// ValidateTransition checks whether a state transition from → to is legal.
func ValidateTransition(from, to JobStatus) error {
	targets, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("%w: unknown source state %s", ErrInvalidTransition, from)
	}
	for _, t := range targets {
		if t == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
}

// transitionJob validates and applies a state transition on a job, updating
// timestamps and restart count as appropriate, then persists the result.
// All mutations are performed under c.mu.Lock() to prevent data races with
// concurrent HTTP handlers reading the same *JobMeta.
func (c *Coordinator) transitionJob(job *JobMeta, to JobStatus) error {
	now := time.Now().UTC()

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := ValidateTransition(job.Status, to); err != nil {
		return err
	}

	// Set StartedAt on first transition to RUNNING.
	if to == JobRunning && job.StartedAt.IsZero() {
		job.StartedAt = now
	}

	// Set FinishedAt on terminal states.
	if to.IsTerminal() {
		job.FinishedAt = now
	}

	// Increment RestartCount on FAILING → DEPLOYING (restart).
	if job.Status == JobFailing && to == JobDeploying {
		job.RestartCount++
	}

	job.Status = to
	job.UpdatedAt = now

	return c.persistJobLocked(job)
}
