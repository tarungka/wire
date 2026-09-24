package coordinator

import (
	"time"

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
	if job.Status != JobFailing || !c.readyLocked() {
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
		if assignment.EpochID < c.epoch && time.Now().Before(c.recoveryFenceUntil) && (worker == nil || worker.LastHeartbeat.IsZero()) {
			// A re-registering worker has joined its prior attempt before
			// asking for a new grant. Otherwise wait out the old contact
			// deadline, even if loss detection already marked tasks FAILED.
			c.mu.RUnlock()
			return false
		}
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
	restarts, updated := job.RecoveryAttempts, job.UpdatedAt
	rescale := job.RescaleRequested
	maxAttempts := c.config.RestartMaxAttempts
	delay := time.Duration(0)
	if restarts > 0 {
		delay = c.config.RestartBackoff * time.Duration(1<<min(restarts-1, 6))
	}
	if job.RestartPolicy != nil {
		maxAttempts = job.RestartPolicy.MaxAttempts
		delay = job.RestartPolicy.DelayAfter(restarts)
	}
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
	// Before the first completed checkpoint, recovery reopens sources at their
	// configured initial position. No transactional output has a commit decision;
	// replacement sinks fence and abort orphan transactions at boundary zero.
	if !rescale && restarts >= maxAttempts {
		if err := c.transitionJob(job, JobFailed); err != nil {
			c.log.Warn().Err(err).Str("job_id", job.ID).Msg("cannot finalize failed job")
		}
		return false
	}
	if !rescale && time.Since(updated) < delay {
		return false
	}
	return true
}

func (c *Coordinator) rollbackFailedRescale(job *JobMeta) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() || job.Status != JobFailing || job.RescaleRollback == nil || !job.RescaleRollback.Attempted {
		return nil
	}
	old := job.RescaleRollback
	next := *job
	next.Config = append([]byte(nil), old.Config...)
	next.Parallelism = old.Parallelism
	next.LatestCheckpoint = old.Checkpoint
	var restoredSecrets jobSecretValues
	if job.ReplacementCheckpoint != 0 {
		var graph rpc.JobGraph
		if err := protocol.DecodeMsgPack(old.Config, &graph); err != nil {
			return err
		}
		var err error
		restoredSecrets, err = resolveJobSecretReferences(graph)
		if err != nil {
			return err
		}
		defer func() { restoredSecrets.clear() }()
		next.CheckpointPolicy, next.RestartPolicy = graph.CheckpointPolicy, graph.RestartPolicy
	}
	next.ReplacementCheckpoint = 0
	next.RescaleCheckpoint = 0
	next.RescaleRequested = false
	next.RescaleFailure = "rescale deployment failed; restoring previous configuration"
	if job.ReplacementCheckpoint != 0 {
		next.RescaleFailure = "replacement deployment failed; restoring previous configuration"
	}
	next.RescaleRollback = nil
	if err := c.persistJobLocked(&next); err != nil {
		return err
	}
	*job = next
	if restoredSecrets != nil {
		c.installJobSecretsLocked(job.ID, restoredSecrets)
		restoredSecrets = nil
	}
	c.jobs[job.ID] = job
	return nil
}

// resetStableRecoveryBudget is called before leaving RUNNING. Callers hold
// c.mu and persist the updated job together with their state transition.
func (c *Coordinator) resetStableRecoveryBudget(job *JobMeta, now time.Time) {
	if job.RestartPolicy == nil && job.Status == JobRunning && !job.RunningSince.IsZero() && now.Sub(job.RunningSince) >= c.config.RestartResetAfter {
		job.RecoveryAttempts = 0
	}
}

// Bound placement retries as well as deployed attempts: a larger layout can
// lose capacity after admission but before the old tasks finish cancellation.
func (c *Coordinator) recordRescalePlacementFailure(job *JobMeta, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() || job.Status != JobFailing || job.RescaleRollback == nil || job.RescaleRollback.Attempted {
		return
	}
	next := *job
	rollback := *job.RescaleRollback
	if rollback.PlacementFailedSince.IsZero() {
		rollback.PlacementFailedSince = now.UTC()
	}
	// Cancellation acknowledgements precede slot updates. Allow two full
	// worker heartbeat intervals after the first failed placement, regardless
	// of scheduler tick frequency, before giving up on the larger topology.
	rollback.Attempted = now.Sub(rollback.PlacementFailedSince) >= 2*c.config.HeartbeatInterval
	next.RescaleRollback = &rollback
	if err := c.persistJobLocked(&next); err != nil {
		c.log.Warn().Err(err).Msg("cannot persist rescale placement failure")
		return
	}
	*job = next
	c.jobs[job.ID] = job
}
