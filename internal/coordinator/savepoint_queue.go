package coordinator

import (
	"errors"
	"sort"
	"time"
)

// queuedSavepointsLocked reads the durable queue. The ownership lock serializes
// enqueue, dispatch and deletion; timestamps preserve arrival order on recovery.
func (c *Coordinator) queuedSavepointsLocked(jobID string) ([]*SavepointMeta, error) {
	if !c.queuedSavepointJobs[jobID] {
		return nil, nil
	}
	points, err := c.ListSavepoints(jobID)
	if err != nil {
		return nil, err
	}
	queued := make([]*SavepointMeta, 0)
	for _, sp := range points {
		if sp.Queued && sp.Status == SavepointInProgress {
			queued = append(queued, sp)
		}
	}
	sort.Slice(queued, func(i, j int) bool {
		if queued[i].TriggerTime.Equal(queued[j].TriggerTime) {
			return queued[i].ID < queued[j].ID
		}
		return queued[i].TriggerTime.Before(queued[j].TriggerTime)
	})
	return queued, nil
}

func (c *Coordinator) scheduleQueuedSavepoints() {
	c.mu.RLock()
	ids := make([]string, 0, len(c.queuedSavepointJobs))
	for id := range c.queuedSavepointJobs {
		ids = append(ids, id)
	}
	c.mu.RUnlock()
	for _, id := range ids {
		if err := c.advanceQueuedSavepoint(id); err != nil && !errors.Is(err, ErrCheckpointInProgress) && !errors.Is(err, ErrJobNotRunning) && !errors.Is(err, ErrSavepointNotFound) {
			c.log.Warn().Err(err).Str("job_id", id).Msg("savepoint queue will retry")
		}
	}
}

func (c *Coordinator) advanceQueuedSavepoint(jobID string) error {
	c.mu.Lock()
	if !c.readyLocked() {
		c.mu.Unlock()
		return ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		c.mu.Unlock()
		return ErrJobNotFound
	}
	// A waiting request needs no metadata scan while another boundary owns
	// the job. Keep the frequent runner off the store until it can dispatch.
	if job.Status == JobRunning {
		if _, active := c.activeCheckpoints[jobID]; active {
			c.mu.Unlock()
			return ErrCheckpointInProgress
		}
	}
	points, err := c.queuedSavepointsLocked(jobID)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if job.Status.IsTerminal() || job.Status == JobCanceling || job.Status == JobPaused {
		for _, sp := range points {
			sp.Queued = false
			sp.Status = SavepointFailed
			sp.CompletionTime = time.Now().UTC()
			if err := c.persistSavepoint(sp); err != nil {
				c.mu.Unlock()
				return err
			}
		}
		delete(c.queuedSavepointJobs, jobID)
		c.mu.Unlock()
		return nil
	}
	if len(points) == 0 {
		delete(c.queuedSavepointJobs, jobID)
	}
	if job.Status != JobRunning || len(points) == 0 {
		c.mu.Unlock()
		return nil
	}
	id := points[0].ID
	c.mu.Unlock()
	_, err = c.triggerCheckpointWithQueue(jobID, id, false, true)
	return err
}
