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
	if c.state != StateLeader || !c.recovered {
		c.mu.Unlock()
		return false
	}
	changed := false
	now := time.Now()
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
	failed := make(map[*JobMeta]bool)
	for _, job := range c.jobs {
		if job.Status != JobRunning && job.Status != JobDeploying && job.Status != JobFailing {
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
			if worker == nil || worker.Lost {
				c.taskStatuses[task] = rpc.TaskStatusFailed
				if job.Status != JobFailing {
					failed[job] = true
				}

			}
		}
	}
	c.mu.Unlock()
	for job := range failed {
		if err := c.transitionJob(job, JobFailing); err != nil {
			c.log.Warn().Err(err).Str("job_id", job.ID).Msg("cannot recover lost worker")
		}
	}
	return changed || len(failed) > 0
}
