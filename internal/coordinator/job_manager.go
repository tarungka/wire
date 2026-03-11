package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// generateJobID returns a unique job identifier in the form "job-<hex32>".
func generateJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
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

	// Encode outside the lock to avoid holding it during serialization.
	data, err := protocol.EncodeMsgPack(job)
	if err != nil {
		return nil, fmt.Errorf("encoding job %s: %w", job.ID, err)
	}

	// Atomic check+insert under a single lock to prevent TOCTOU races:
	// two concurrent SubmitJob calls with the same name could both pass a
	// separate read-only duplicate check before either persists.
	c.mu.Lock()
	for _, j := range c.jobs {
		if j.Name == name && !j.Status.IsTerminal() {
			c.mu.Unlock()
			return nil, fmt.Errorf("%w: active job with name %q", ErrJobExists, name)
		}
	}
	if err := c.store.Set(JobMetaKey(job.ID), data); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("persisting job %s: %w", job.ID, err)
	}
	c.jobs[job.ID] = job
	c.mu.Unlock()

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

	// Load task assignments and enqueue cancel commands to workers.
	data, err := c.store.Get(JobAssignmentsKey(jobID))
	if err == nil && data != nil {
		var tam TaskAssignmentMap
		if err := protocol.DecodeMsgPack(data, &tam); err == nil {
			for taskID, workerID := range tam.Assignments {
				c.EnqueueCommand(workerID, rpc.WorkerCommand{
					Type:   rpc.CommandTypeCancelTask,
					JobID:  jobID,
					TaskID: taskID,
				})
			}
		}
	}

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

	if err := c.transitionJob(job, JobDeploying); err != nil {
		return nil, err
	}

	c.log.Info().Str("job_id", jobID).Msg("job resumed")
	// TODO: re-deploy from savepoint
	return job, nil
}
