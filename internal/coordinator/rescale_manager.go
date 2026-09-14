package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// RescaleJob redeploys a running job from its latest completed savepoint.
// The existing fenced restart path cancels and waits for old tasks before
// scheduling the new ownership map. A later checkpoint must not be rolled back.
func (c *Coordinator) RescaleJob(jobID, savepointID string, parallelism int) (*JobMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateLeader || !c.recovered {
		return nil, ErrNotLeader
	}
	job, ok := c.jobs[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	if job.Status != JobRunning {
		return nil, fmt.Errorf("%w: rescale requires a running job", ErrInvalidTransition)
	}
	if c.activeCheckpoints[jobID].ID != 0 {
		return nil, ErrCheckpointInProgress
	}
	sp, err := c.GetSavepoint(jobID, savepointID)
	if err != nil {
		return nil, err
	}
	if sp.Status != SavepointCompleted || sp.CheckpointID == 0 || sp.CheckpointID != job.LatestCheckpoint {
		return nil, fmt.Errorf("%w: rescale requires the latest completed checkpoint to be a savepoint", ErrInvalidTransition)
	}
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(job.Config, &graph); err != nil {
		return nil, err
	}
	for i := range graph.Operators {
		graph.Operators[i].Parallelism = int32(parallelism)
	}
	if _, err := validateGraphKeyGroups(graph, parallelism); err != nil {
		return nil, err
	}
	config, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		return nil, err
	}
	candidate := *job
	candidate.Config = config
	candidate.Parallelism = parallelism
	candidate.RescaleCheckpoint = sp.CheckpointID
	candidate.RescaleRequested = true
	if !job.RunningSince.IsZero() && time.Since(job.RunningSince) >= c.config.RestartResetAfter {
		candidate.RecoveryAttempts = 0
	}
	candidate.UpdatedAt = time.Now().UTC()
	tasks, err := generateTaskDescriptors(&candidate)
	if err != nil {
		return nil, err
	}
	if err := c.attachCheckpointRestoreLocked(&candidate, map[string][]rpc.TaskDescriptor{"validation": tasks}); err != nil {
		return nil, err
	}
	// Publish the new graph and selected snapshot together; restart remains
	// blocked by the old persisted assignment until all its tasks terminate.
	candidate.Status = JobFailing
	if err := c.persistJobLocked(&candidate); err != nil {
		return nil, err
	}
	job.Config = candidate.Config
	job.Parallelism = candidate.Parallelism
	job.RescaleCheckpoint = candidate.RescaleCheckpoint
	job.RescaleRequested = candidate.RescaleRequested
	job.RecoveryAttempts = candidate.RecoveryAttempts
	job.UpdatedAt = candidate.UpdatedAt
	job.Status = candidate.Status
	c.jobs[jobID] = job
	result := candidate
	return &result, nil
}
