package coordinator

import (
	"context"
	"errors"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

var errFinalCheckpointNotReady = errors.New("sources are not ready for a final checkpoint")

// Called under the ownership lock; FINISHING reports are attempt-fenced.
func (c *Coordinator) sourcesExhaustedLocked(assignment TaskAssignmentMap) bool {
	sources := 0
	for _, task := range assignment.TaskDescriptors {
		for _, operator := range task.OperatorChain {
			if operator.Type == rpc.OperatorTypeSource {
				sources++
				if c.taskStatuses[task.TaskID] != rpc.TaskStatusFinishing {
					return false
				}
				break
			}
		}
	}
	return sources > 0
}

func (c *Coordinator) scheduleFinalCheckpoints(ctx context.Context) {
	c.mu.RLock()
	var jobs []string
	for id, job := range c.jobs {
		if job.Status != JobRunning {
			continue
		}
		jobs = append(jobs, id)
	}
	c.mu.RUnlock()
	for _, id := range jobs {
		if ctx.Err() != nil {
			return
		}
		data, err := c.store.Get(JobAssignmentsKey(id))
		var assignment TaskAssignmentMap
		if err != nil || protocol.DecodeMsgPack(data, &assignment) != nil {
			continue
		}
		c.mu.RLock()
		ready := c.sourcesExhaustedLocked(assignment)
		c.mu.RUnlock()
		if !ready {
			continue
		}
		// Recheck readiness and assignment while persisting the checkpoint decision.
		if _, err := c.triggerCheckpointBoundary(id, "", true); err != nil && !errors.Is(err, ErrCheckpointInProgress) && !errors.Is(err, errFinalCheckpointNotReady) && !errors.Is(err, ErrJobNotRunning) {
			c.log.Warn().Err(err).Str("job_id", id).Msg("final checkpoint could not start")
			if errors.Is(err, ErrCheckpointUnavailable) {
				c.mu.RLock()
				job := c.jobs[id]
				c.mu.RUnlock()
				if job != nil {
					_ = c.transitionJob(job, JobFailing)
				}
			}
		}
	}
}
