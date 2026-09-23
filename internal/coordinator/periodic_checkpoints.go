package coordinator

import (
	"context"
	"time"
)

// Keep checkpoint RPCs off the scheduler and heartbeat loops. This runner is
// joined on leadership exit; triggerCheckpoint rechecks leadership and status.
func (c *Coordinator) runPeriodicCheckpoints(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			c.expireCheckpoints(now)
			for _, jobID := range c.duePeriodicCheckpoints(now) {
				if ctx.Err() != nil {
					return
				}
				if _, err := c.TriggerCheckpoint(jobID); err != nil {
					c.log.Debug().Err(err).Str("job_id", jobID).Msg("periodic checkpoint deferred")
				}
			}
		}
	}
}

func (c *Coordinator) duePeriodicCheckpoints(now time.Time) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.readyLocked() {
		return nil
	}
	var due []string
	for id, job := range c.jobs {
		policy := job.CheckpointPolicy
		if job.Status != JobRunning || policy == nil || policy.Interval <= 0 {
			continue
		}
		if _, active := c.activeCheckpoints[id]; active {
			continue
		}
		anchor := job.RunningSince
		if job.LastCheckpointTrigger.After(anchor) {
			anchor = job.LastCheckpointTrigger
		}
		if anchor.IsZero() || now.Sub(anchor) < policy.Interval {
			continue
		}
		if !job.LastCheckpointCompletion.IsZero() && now.Sub(job.LastCheckpointCompletion) < policy.MinPause {
			continue
		}
		due = append(due, id)
	}
	return due
}
