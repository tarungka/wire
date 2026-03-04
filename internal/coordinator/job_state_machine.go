package coordinator

import (
	"fmt"
	"time"
)

// validTransitions defines the set of legal job state transitions.
var validTransitions = map[JobStatus][]JobStatus{
	JobCreated:   {JobDeploying},
	JobDeploying: {JobRunning, JobFailing},
	JobRunning:   {JobFinishing, JobPaused, JobFailing, JobCanceling},
	JobPaused:    {JobDeploying},
	JobFinishing: {JobFinished},
	JobFailing:   {JobDeploying, JobCanceled},
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
func (c *Coordinator) transitionJob(job *JobMeta, to JobStatus) error {
	if err := ValidateTransition(job.Status, to); err != nil {
		return err
	}

	now := time.Now().UTC()

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

	return c.persistJob(job)
}
