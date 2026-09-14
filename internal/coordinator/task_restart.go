package coordinator

import (
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// prepareTaskRestart waits for every old task to report a terminal state before
// permitting deployment. Cancellation is fenced to the persisted old attempt.
func (c *Coordinator) prepareTaskRestart(job *JobMeta) bool {
	if err := c.rollbackFailedRescale(job); err != nil {
		c.log.Warn().Err(err).Msg("cannot restore pre-rescale configuration")
		return false
	}
	c.mu.RLock()
	if job.Status != JobFailing || c.state != StateLeader || !c.recovered {
		c.mu.RUnlock()
		return false
	}
	data, err := c.store.Get(JobAssignmentsKey(job.ID))
	var assignment TaskAssignmentMap
	if err != nil || protocol.DecodeMsgPack(data, &assignment) != nil || assignment.JobID != job.ID {
		c.mu.RUnlock()
		return false
	}
	var cancelTasks []rpc.WorkerCommand
	var workers []string
	for taskID, workerID := range assignment.Assignments {
		switch c.taskStatuses[taskID] {
		case rpc.TaskStatusFailed, rpc.TaskStatusFinished, rpc.TaskStatusCanceled:
		default:
			workers = append(workers, workerID)
			cancelTasks = append(cancelTasks, rpc.WorkerCommand{Type: rpc.CommandTypeCancelTask, JobID: job.ID, TaskID: taskID, EpochID: assignment.EpochID, AttemptID: assignment.AttemptID})
		}
	}
	checkpoint := c.activeCheckpoints[job.ID]
	latest := job.LatestCheckpoint
	c.mu.RUnlock()
	if checkpoint.ID != 0 {
		if err := c.AbortCheckpoint(job.ID, checkpoint.ID, checkpoint.EpochID); err != nil {
			return false
		}
	}
	for i, command := range cancelTasks {
		c.EnqueueCommand(workers[i], command)
	}
	if len(cancelTasks) > 0 {
		return false
	}
	if latest == 0 {
		if err := c.transitionJob(job, JobFailed); err != nil {
			c.log.Warn().Err(err).Str("job_id", job.ID).Msg("cannot finalize failed job")
		}
		return false
	}
	return true
}

func (c *Coordinator) rollbackFailedRescale(job *JobMeta) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateLeader || !c.recovered || job.Status != JobFailing || job.RescaleRollback == nil || !job.RescaleRollback.Attempted {
		return nil
	}
	old := job.RescaleRollback
	next := *job
	next.Config = append([]byte(nil), old.Config...)
	next.Parallelism = old.Parallelism
	next.LatestCheckpoint = old.Checkpoint
	next.RescaleCheckpoint = 0
	next.RescaleRollback = nil
	if err := c.persistJobLocked(&next); err != nil {
		return err
	}
	*job = next
	return nil
}
