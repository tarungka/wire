package coordinator

import (
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// prepareTaskRestart waits for every old task to report a terminal state before
// permitting deployment. Cancellation is fenced to the persisted old attempt.
func (c *Coordinator) prepareTaskRestart(job *JobMeta) bool {
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
		worker := c.workers[workerID]
		if worker == nil || worker.LastHeartbeat.IsZero() || time.Since(worker.LastHeartbeat) >= c.config.WorkerTimeout {
			continue
		}
		switch c.taskStatuses[taskID] {
		case rpc.TaskStatusFailed, rpc.TaskStatusFinished, rpc.TaskStatusCanceled:
		default:
			workers = append(workers, workerID)
			cancelTasks = append(cancelTasks, rpc.WorkerCommand{Type: rpc.CommandTypeCancelTask, JobID: job.ID, TaskID: taskID, EpochID: assignment.EpochID, AttemptID: assignment.AttemptID})
		}
	}
	checkpoint := c.activeCheckpoints[job.ID]
	latest := job.LatestCheckpoint
	restarts, updated := job.RecoveryAttempts, job.UpdatedAt
	rescale := job.RescaleRequested
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
	if latest == 0 || (!rescale && restarts >= c.config.RestartMaxAttempts) {
		if err := c.transitionJob(job, JobFailed); err != nil {
			c.log.Warn().Err(err).Str("job_id", job.ID).Msg("cannot finalize failed job")
		}
		return false
	}
	if !rescale && restarts > 0 && time.Since(updated) < c.config.RestartBackoff*time.Duration(1<<min(restarts-1, 6)) {
		return false
	}
	return true
}

// resetStableRecoveryBudget is called before leaving RUNNING. Callers hold
// c.mu and persist the updated job together with their state transition.
func (c *Coordinator) resetStableRecoveryBudget(job *JobMeta, now time.Time) {
	if job.Status == JobRunning && !job.RunningSince.IsZero() && now.Sub(job.RunningSince) >= c.config.RestartResetAfter {
		job.RecoveryAttempts = 0
	}
}
