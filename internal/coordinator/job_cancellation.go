package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func (c *Coordinator) scheduleCancellations() {
	c.mu.RLock()
	var ids []string
	for id, job := range c.jobs {
		if job.Status == JobCanceling {
			ids = append(ids, id)
		}
	}
	c.mu.RUnlock()
	for _, id := range ids {
		if err := c.advanceCancellation(id, time.Now()); err != nil {
			c.log.Warn().Err(err).Str("job_id", id).Msg("job cancellation will retry")
		}
	}
}

func (c *Coordinator) advanceCancellation(jobID string, now time.Time) error {
	// Abort the snapshot before cancel commands. Both decisions survive restart;
	// neither a failed write nor an unacknowledged cancel finishes the job.
	c.mu.RLock()
	checkpoint := c.activeCheckpoints[jobID]
	c.mu.RUnlock()
	if checkpoint.ID != 0 {
		if err := c.AbortCheckpoint(jobID, checkpoint.ID, checkpoint.EpochID); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil || job.Status != JobCanceling {
		return nil
	}
	data, err := c.store.Get(JobAssignmentsKey(jobID))
	if err != nil {
		return err
	}
	var assignment TaskAssignmentMap
	if len(data) != 0 {
		if err := protocol.DecodeMsgPack(data, &assignment); err != nil {
			return err
		}
		if assignment.JobID != jobID {
			return fmt.Errorf("cancellation assignment identity mismatch")
		}
	}
	stopped := true
	for taskID, workerID := range assignment.Assignments {
		worker := c.workers[workerID]
		if assignment.EpochID < c.epoch && now.Before(c.recoveryFenceUntil) && (worker == nil || worker.LastHeartbeat.IsZero()) {
			stopped = false
			continue
		}
		if worker == nil {
			// A deleted registration is not proof its process stopped. Allow its last
			// possible contact lease to expire before claiming terminal cancellation.
			if now.Before(job.UpdatedAt.Add(c.config.WorkerTimeout)) {
				stopped = false
			}
			continue
		}
		if worker.LastHeartbeat.IsZero() || now.Sub(worker.LastHeartbeat) >= c.config.WorkerTimeout {
			continue
		}
		switch c.taskStatuses[taskID] {
		case rpc.TaskStatusCanceled, rpc.TaskStatusFailed, rpc.TaskStatusFinished:
			continue
		}
		stopped = false
		c.enqueueCommandLocked(workerID, rpc.WorkerCommand{Type: rpc.CommandTypeCancelTask, JobID: jobID, TaskID: taskID, EpochID: assignment.EpochID, AttemptID: assignment.AttemptID})
	}
	if !stopped {
		return nil
	}
	return c.transitionJobLocked(job, JobCanceled)
}
