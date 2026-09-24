package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// PauseJob atomically queues a savepoint and records pause intent. Processing
// continues until that checkpoint completes; PAUSED is published after teardown.
func (c *Coordinator) PauseJob(jobID string) (*JobMeta, *SavepointMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return nil, nil, ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		return nil, nil, ErrJobNotFound
	}
	if job.Status != JobRunning {
		return nil, nil, ErrJobNotRunning
	}
	if job.PauseSavepointID != "" {
		sp, err := c.GetSavepoint(jobID, job.PauseSavepointID)
		if err != nil {
			return nil, nil, err
		}
		if sp.Status == SavepointInProgress {
			snapshot := *job
			return &snapshot, sp, nil
		}
	}
	sp := &SavepointMeta{ID: generateSavepointID(), JobID: jobID, Status: SavepointInProgress, Queued: true, TriggerTime: time.Now().UTC()}
	next := *job
	next.PauseSavepointID = sp.ID
	next.PauseFailure = ""
	jobData, err := protocol.EncodeMsgPack(&next)
	if err != nil {
		return nil, nil, err
	}
	spData, err := protocol.EncodeMsgPack(sp)
	if err != nil {
		return nil, nil, err
	}
	if err := c.store.WriteBatch([]KVPair{{Key: JobMetaKey(jobID), Value: jobData}, {Key: SavepointKey(jobID, sp.ID), Value: spData}}); err != nil {
		return nil, nil, err
	}
	*job = next
	c.queuedSavepointJobs[jobID] = true
	return &next, sp, nil
}

func (c *Coordinator) schedulePauses() {
	c.mu.RLock()
	var ids []string
	for id, job := range c.jobs {
		if job.Status == JobPausing || (job.Status != JobPaused && job.Status != JobResuming && !job.Status.IsTerminal() && job.PauseSavepointID != "") {
			ids = append(ids, id)
		}
	}
	c.mu.RUnlock()
	for _, id := range ids {
		if err := c.advancePause(id, time.Now()); err != nil {
			c.log.Warn().Err(err).Str("job_id", id).Msg("pause reconciliation will retry")
		}
	}
}

func (c *Coordinator) advancePause(jobID string, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		return ErrJobNotFound
	}
	if job.Status == JobPausing {
		stopped, err := c.stopAssignedTasksLocked(job, now)
		if err != nil {
			return err
		}
		if !stopped {
			return nil
		}
		return c.transitionJobLocked(job, JobPaused)
	}
	if job.PauseSavepointID == "" || job.Status.IsTerminal() || job.Status == JobCanceling || job.Status == JobPaused || job.Status == JobResuming {
		return nil
	}
	sp, err := c.GetSavepoint(jobID, job.PauseSavepointID)
	if err != nil {
		return err
	}
	if sp.Status == SavepointFailed {
		next := *job
		next.PauseSavepointID = ""
		next.PauseFailure = "pause savepoint failed; job was not paused"
		if err := c.persistJobLocked(&next); err != nil {
			return err
		}
		*job = next
		c.jobs[jobID] = job
	}
	return nil
}

// ResumeJob schedules a new deployment using the exact completed pause boundary.
// RESUMING waits for capacity; it does not spend a failure-recovery attempt.
func (c *Coordinator) ResumeJob(jobID string) (*JobMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return nil, ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		return nil, ErrJobNotFound
	}
	if job.Status != JobPaused {
		return nil, ErrJobNotPaused
	}
	if job.PauseCheckpoint == 0 || job.LatestCheckpoint != job.PauseCheckpoint {
		return nil, fmt.Errorf("%w: paused job has no pinned checkpoint", ErrInvalidConfig)
	}
	if _, _, err := c.selectRecoveryCheckpointLocked(job); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCheckpointUnavailable, err)
	}
	if err := c.transitionJobLocked(job, JobResuming); err != nil {
		return nil, err
	}
	snapshot := *job
	c.kickScheduler()
	return &snapshot, nil
}
