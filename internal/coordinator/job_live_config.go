package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// SetCheckpointInterval updates future periodic checkpoint scheduling without
// changing deployment identity or interrupting an in-flight checkpoint. Zero
// disables periodic triggers; manual checkpoints and savepoints remain enabled.
func (c *Coordinator) SetCheckpointInterval(jobID string, interval time.Duration) (*JobMeta, error) {
	return c.setCheckpointInterval(jobID, interval, nil)
}

func (c *Coordinator) setCheckpointInterval(jobID string, interval time.Duration, expected *time.Duration) (*JobMeta, error) {
	if interval < 0 {
		return nil, fmt.Errorf("%w: checkpoint interval must be nonnegative", ErrInvalidConfig)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return nil, ErrNotLeader
	}
	job, ok := c.jobs[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	if job.Status != JobRunning {
		return nil, ErrJobNotRunning
	}
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(job.Config, &graph); err != nil {
		return nil, fmt.Errorf("%w: live updates require a structured graph", ErrInvalidConfig)
	}
	policy := rpc.CheckpointPolicy{Timeout: c.config.CheckpointTimeout, MinPause: c.config.CheckpointMinPause}
	if job.CheckpointPolicy != nil {
		policy = *job.CheckpointPolicy
	}
	if expected != nil && policy.Interval != *expected {
		return nil, fmt.Errorf("%w: checkpoint interval changed since last observation", ErrInvalidTransition)
	}
	policy.Interval = interval
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	graph.CheckpointPolicy = &policy
	config, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		return nil, err
	}
	next := *job
	next.Config, next.CheckpointPolicy, next.UpdatedAt = config, &policy, time.Now().UTC()
	data, err := protocol.EncodeMsgPack(next)
	if err != nil {
		return nil, err
	}
	if err := c.store.WriteBatch([]KVPair{{Key: JobMetaKey(jobID), Value: data}, {Key: JobConfigKey(jobID), Value: config}}); err != nil {
		return nil, err
	}
	*job = next
	return &next, nil
}
