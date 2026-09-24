package coordinator

import (
	"time"

	"github.com/tarungka/wire/internal/observability"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// Worker contact expires its old execution authority. Live workers still need
// to acknowledge cancellation before prepareTaskRestart permits redeployment.
func (c *Coordinator) detectLostTaskWorkers() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return false
	}
	now := time.Now()
	changed := c.expireWorkersLocked(now)
	failed := make(map[*JobMeta]bool)
	for _, job := range c.jobs {
		if job.Status != JobRunning && job.Status != JobDeploying && job.Status != JobFailing && job.Status != JobFinishing {
			continue
		}
		data, err := c.store.Get(JobAssignmentsKey(job.ID))
		var assignment TaskAssignmentMap
		if err != nil || protocol.DecodeMsgPack(data, &assignment) != nil || assignment.JobID != job.ID {
			continue
		}
		for task, owner := range assignment.Assignments {
			switch c.taskStatuses[task] {
			case rpc.TaskStatusFinished:
				continue
			}
			worker := c.workers[owner]
			if worker != nil && worker.Removed && job.Status != JobFailing {
				// Removal requests cancellation, but is not proof execution stopped.
				// Keep nonterminal task status until its report or lease expiry.
				failed[job] = true
			}
			if worker == nil || worker.Lost {
				c.taskStatuses[task] = rpc.TaskStatusFailed
				if job.Status != JobFailing {
					failed[job] = true
				}

			}
		}
	}
	// Publish failure before releasing the ownership lock. Otherwise a quick
	// re-registration/redeploy could replace this attempt between detection and
	// transition, and this old loss notification would fail the new attempt.
	for job := range failed {
		next := *job
		c.resetStableRecoveryBudget(&next, now)
		next.Status = JobFailing
		next.UpdatedAt = now.UTC()
		if err := c.persistJobLocked(&next); err != nil {
			c.log.Warn().Err(err).Str("job_id", job.ID).Msg("cannot recover lost worker")
			continue
		}
		*job = next
		c.jobs[job.ID] = job
	}
	return changed || len(failed) > 0
}

// expireTaskWorkers is the health timer and heartbeat rejection fast path. It
// only fences in-memory authority; the scheduler handles assignment reads and
// durable job transitions under the ownership lock.
func (c *Coordinator) expireTaskWorkers() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return false
	}
	return c.expireWorkersLocked(time.Now())
}

func (c *Coordinator) expireWorkersLocked(now time.Time) bool {
	changed := false
	for _, worker := range c.workers {
		if !worker.Lost && (worker.LastHeartbeat.IsZero() || now.Sub(worker.LastHeartbeat) >= c.config.WorkerTimeout) {
			worker.Lost = true
			changed = true
			worker.TaskSlotsAvailable = 0
			if !worker.LastHeartbeat.IsZero() {
				(observability.HeartbeatMetrics{}).IncWorkersLostTotal()
			}
		}
	}
	return changed
}
