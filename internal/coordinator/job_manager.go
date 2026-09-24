package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
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

	// Legacy opaque configurations remain accepted. Structured graphs are
	// validated before reserving a job name or writing any metadata.
	var defaultErr error
	config, defaultErr = c.resolveStateBackendDefaults(config)
	if defaultErr != nil {
		return nil, defaultErr
	}
	var graph rpc.JobGraph
	var secrets jobSecretValues
	installed := false
	defer func() {
		if !installed {
			secrets.clear()
		}
	}()
	var checkpointPolicy *rpc.CheckpointPolicy
	var restartPolicy *rpc.RestartPolicy
	if err := protocol.DecodeMsgPack(config, &graph); err == nil {
		var err error
		secrets, err = resolveJobSecretReferences(graph)
		if err != nil {
			return nil, err
		}
		if err := graph.RestartPolicy.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if err := graph.CheckpointPolicy.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if _, err := validateGraphKeyGroups(graph, parallelism); err != nil {
			return nil, err
		}
		checkpointPolicy = graph.CheckpointPolicy
		restartPolicy = graph.RestartPolicy
	}

	now := time.Now().UTC()
	job := &JobMeta{
		CheckpointPolicy: checkpointPolicy,
		RestartPolicy:    restartPolicy,
		ID:               generateJobID(),
		Name:             name,
		Status:           JobCreated,
		Parallelism:      parallelism,
		Config:           config,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// Encode outside the lock to avoid holding it during serialization.
	data, err := protocol.EncodeMsgPack(job)
	if err != nil {
		return nil, fmt.Errorf("encoding job %s: %w", job.ID, err)
	}

	// Reserve the name + ID in c.jobs under the lock, then persist
	// outside the lock so worker RPCs (Heartbeat, UpdateTaskStatus) do
	// NOT queue behind submit fsync. The reservation is what serialises
	// concurrent same-name submits — the duplicate check sees the
	// already-inserted entry and rejects.
	//
	// Trade-off: a crash between the in-memory insert and the disk
	// commit drops the unacknowledged reservation. Success is returned only
	// after the synchronous batch commits. A crash after commit but before
	// the response can leave the client uncertain; recovery retains the job.
	c.mu.Lock()
	if _, exists := c.activeJobNames[name]; exists {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: active job with name %q", ErrJobExists, name)
	}
	c.activeJobNames[name] = job.ID
	c.jobs[job.ID] = job
	c.installJobSecretsLocked(job.ID, secrets)
	installed = true
	c.mu.Unlock()

	// Persist meta + config in a single WriteBatch so the submit pays
	// one fsync, not two. Recovery already treats the pair as a unit
	// (a meta row without a config row is rejected by the scheduler),
	// so atomic batching matches that invariant.
	if err := c.store.WriteBatch([]KVPair{
		{Key: JobMetaKey(job.ID), Value: data},
		{Key: JobConfigKey(job.ID), Value: config},
	}); err != nil {
		// Roll back the reservation so a retry can succeed and the
		// in-memory state stays consistent with disk.
		c.mu.Lock()
		delete(c.jobs, job.ID)
		c.forgetJobSecretsLocked(job.ID)
		delete(c.activeJobNames, name)
		c.mu.Unlock()
		return nil, fmt.Errorf("persisting job %s: %w", job.ID, err)
	}

	c.log.Info().Str("job_id", job.ID).Str("name", name).Int("parallelism", parallelism).Msg("job submitted")

	// Snapshot BEFORE kicking the scheduler — the scheduler mutates *job
	// under c.mu, and the HTTP submit handler reads through this returned
	// pointer. Taking a copy after the kick races the immediate
	// scheduleJob write.
	c.mu.RLock()
	snapshot := *job
	snapshot.TransactionTaskIDs = maps.Clone(job.TransactionTaskIDs)
	c.mu.RUnlock()

	// Now wake the scheduler so it dispatches this job in the next
	// goroutine turn instead of waiting up to 2s for the next tick.
	c.kickScheduler()

	return &snapshot, nil
}

// GetJob retrieves a job by ID from the in-memory cache.
func (c *Coordinator) GetJob(jobID string) (*JobMeta, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	job, ok := c.jobs[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	// Return a copy: the live *JobMeta is mutated under c.mu by the
	// scheduler/job manager; callers (HTTP handlers especially) read it
	// after this method returns, outside the lock. Without this snapshot
	// the read races with concurrent Status/UpdatedAt writes.
	snapshot := *job
	snapshot.TransactionTaskIDs = maps.Clone(job.TransactionTaskIDs)
	return &snapshot, nil
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
		// Snapshot per the same reasoning as GetJob.
		snapshot := *j
		snapshot.TransactionTaskIDs = maps.Clone(j.TransactionTaskIDs)
		result = append(result, &snapshot)
	}
	return result
}

// CancelJob durably requests cancellation. The scheduler retries cancellation
// until all old tasks have stopped; only then does it publish CANCELED.
func (c *Coordinator) CancelJob(jobID string) (*JobMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return nil, ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		return nil, ErrJobNotFound
	}
	if job.Status != JobCanceling {
		if err := c.transitionJobLocked(job, JobCanceling); err != nil {
			return nil, err
		}
	}
	snapshot := *job
	snapshot.TransactionTaskIDs = maps.Clone(job.TransactionTaskIDs)
	c.kickScheduler()
	return &snapshot, nil
}
