package coordinator

import (
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// Worker contact expires its old execution authority. Live workers still need
// to acknowledge cancellation before prepareTaskRestart permits redeployment.
func (c *Coordinator) detectLostTaskWorkers() {
	c.mu.RLock()
	if c.state != StateLeader || !c.recovered {
		c.mu.RUnlock()
		return
	}
	var failed []*JobMeta
	for _, job := range c.jobs {
		if job.Status != JobRunning && job.Status != JobDeploying {
			continue
		}
		data, err := c.store.Get(JobAssignmentsKey(job.ID))
		var assignment TaskAssignmentMap
		if err != nil || protocol.DecodeMsgPack(data, &assignment) != nil || assignment.JobID != job.ID {
			continue
		}
		for task, owner := range assignment.Assignments {
			switch c.taskStatuses[task] {
			case rpc.TaskStatusFinished, rpc.TaskStatusFailed, rpc.TaskStatusCanceled:
				continue
			}
			worker := c.workers[owner]
			if worker == nil || worker.LastHeartbeat.IsZero() || time.Since(worker.LastHeartbeat) >= c.config.WorkerTimeout {
				failed = append(failed, job)
				break
			}
		}
	}
	c.mu.RUnlock()
	for _, job := range failed {
		if err := c.transitionJob(job, JobFailing); err != nil {
			c.log.Warn().Err(err).Str("job_id", job.ID).Msg("cannot recover lost worker")
		}
	}
}
