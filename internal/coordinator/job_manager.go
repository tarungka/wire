package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// generateJobID returns a unique job identifier in the form "job-<hex8>".
func generateJobID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "job-" + hex.EncodeToString(b)
}

// SubmitJob creates a new job and persists it along with its raw configuration.
func (c *Coordinator) SubmitJob(name string, parallelism int, config []byte) (*JobMeta, error) {
	if !c.IsReady() {
		return nil, ErrNotLeader
	}
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidConfig)
	}
	if parallelism < 1 {
		return nil, fmt.Errorf("%w: parallelism must be >= 1", ErrInvalidConfig)
	}

	// Check for duplicate active job name.
	c.mu.RLock()
	for _, j := range c.jobs {
		if j.Name == name && !j.Status.IsTerminal() {
			c.mu.RUnlock()
			return nil, fmt.Errorf("%w: active job with name %q", ErrJobExists, name)
		}
	}
	c.mu.RUnlock()

	now := time.Now().UTC()
	job := &JobMeta{
		ID:          generateJobID(),
		Name:        name,
		Status:      JobCreated,
		Parallelism: parallelism,
		Config:      config,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := c.persistJob(job); err != nil {
		return nil, err
	}

	// Persist raw config separately for large configs.
	if err := c.store.Set(JobConfigKey(job.ID), config); err != nil {
		return nil, fmt.Errorf("persisting config for job %s: %w", job.ID, err)
	}

	c.log.Info().Str("job_id", job.ID).Str("name", name).Int("parallelism", parallelism).Msg("job submitted")
	return job, nil
}

// GetJob retrieves a job by ID from the in-memory cache.
func (c *Coordinator) GetJob(jobID string) (*JobMeta, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	job, ok := c.jobs[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	return job, nil
}

// ListJobs returns all jobs, optionally filtered by status.
func (c *Coordinator) ListJobs(statusFilter *JobStatus) []*JobMeta {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []*JobMeta
	for _, j := range c.jobs {
		if statusFilter != nil && j.Status != *statusFilter {
			continue
		}
		result = append(result, j)
	}
	return result
}

// CancelJob transitions a job to the CANCELING state.
func (c *Coordinator) CancelJob(jobID string) (*JobMeta, error) {
	if !c.IsReady() {
		return nil, ErrNotLeader
	}

	c.mu.RLock()
	job, ok := c.jobs[jobID]
	c.mu.RUnlock()
	if !ok {
		return nil, ErrJobNotFound
	}

	if err := c.transitionJob(job, JobCanceling); err != nil {
		return nil, err
	}

	c.log.Info().Str("job_id", jobID).Msg("job canceling")
	// TODO: issue CmdCancelTask to workers
	return job, nil
}

// PauseJob pauses a running job by triggering a savepoint and transitioning to PAUSED.
func (c *Coordinator) PauseJob(jobID string) (*JobMeta, *SavepointMeta, error) {
	if !c.IsReady() {
		return nil, nil, ErrNotLeader
	}

	c.mu.RLock()
	job, ok := c.jobs[jobID]
	c.mu.RUnlock()
	if !ok {
		return nil, nil, ErrJobNotFound
	}

	if job.Status != JobRunning {
		return nil, nil, ErrJobNotRunning
	}

	sp, err := c.TriggerSavepoint(jobID)
	if err != nil {
		return nil, nil, fmt.Errorf("triggering savepoint for pause: %w", err)
	}

	if err := c.transitionJob(job, JobPaused); err != nil {
		return nil, nil, err
	}

	c.log.Info().Str("job_id", jobID).Str("savepoint_id", sp.ID).Msg("job paused")
	return job, sp, nil
}

// ResumeJob resumes a paused job by transitioning back to DEPLOYING.
func (c *Coordinator) ResumeJob(jobID string) (*JobMeta, error) {
	if !c.IsReady() {
		return nil, ErrNotLeader
	}

	c.mu.RLock()
	job, ok := c.jobs[jobID]
	c.mu.RUnlock()
	if !ok {
		return nil, ErrJobNotFound
	}

	if job.Status != JobPaused {
		return nil, ErrJobNotPaused
	}

	if err := c.transitionJob(job, JobDeploying); err != nil {
		return nil, err
	}

	c.log.Info().Str("job_id", jobID).Msg("job resumed")
	// TODO: re-deploy from savepoint
	return job, nil
}
