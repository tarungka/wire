package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// RescaleJob redeploys a running job from its latest completed savepoint.
// The existing fenced restart path cancels and waits for old tasks before
// scheduling the new ownership map. A later checkpoint must not be rolled back.
func (c *Coordinator) RescaleJob(jobID, savepointID string, parallelism int) (*JobMeta, error) {
	return c.rescaleJob(jobID, savepointID, parallelism, nil)
}

// RescaleOperators changes only explicitly named operators. Sources and sinks
// require an explicit entry, so ordinary scaling cannot multiply their inputs.
func (c *Coordinator) RescaleOperators(jobID, savepointID string, operators map[string]int) (*JobMeta, error) {
	if len(operators) == 0 {
		return nil, fmt.Errorf("%w: operator parallelism is required", ErrInvalidConfig)
	}
	return c.rescaleJob(jobID, savepointID, 0, operators)
}

func (c *Coordinator) rescaleJob(jobID, savepointID string, parallelism int, operators map[string]int) (*JobMeta, error) {
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
	if operators != nil {
		parallelism = job.Parallelism
	}
	if parallelism < 1 {
		return nil, fmt.Errorf("%w: parallelism must be positive", ErrInvalidConfig)
	}
	pinned := forwardBoundaryParallelism(graph, job.Parallelism)
	found := make(map[string]bool)
	for i := range graph.Operators {
		op := &graph.Operators[i]
		if operators != nil {
			if p, ok := operators[op.OperatorID]; ok {
				if p < 1 || p > keygroup.MaxKeyGroups {
					return nil, fmt.Errorf("%w: invalid operator parallelism", ErrInvalidConfig)
				}
				op.Parallelism = int32(p)
				found[op.OperatorID] = true
			}
			continue
		}
		if p, ok := pinned[op.OperatorID]; ok {
			op.Parallelism = p
		} else {
			op.Parallelism = int32(parallelism)
		}
	}
	if len(found) != len(operators) {
		return nil, fmt.Errorf("%w: unknown rescale operator", ErrInvalidConfig)
	}
	if _, err := validateGraphKeyGroups(graph, parallelism); err != nil {
		return nil, err
	}
	config, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		return nil, err
	}
	candidate := *job
	candidate.RescaleFailure = ""
	candidate.RescaleRollback = &RescaleRollback{Config: append([]byte(nil), job.Config...), Parallelism: job.Parallelism, Checkpoint: job.LatestCheckpoint}
	candidate.Config = config
	candidate.Parallelism = parallelism
	candidate.RescaleCheckpoint = sp.CheckpointID
	candidate.RescaleRequested = true
	c.resetStableRecoveryBudget(&candidate, time.Now())
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
	job.RescaleFailure = candidate.RescaleFailure
	job.RescaleRollback = candidate.RescaleRollback
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

// Forward components must move together. A component touching a source or sink
// keeps its current count; shuffle-separated processing components may scale.
func forwardBoundaryParallelism(graph rpc.JobGraph, defaultParallelism int) map[string]int32 {
	neighbors := make(map[string][]string)
	for _, edge := range graph.Edges {
		if edge.Shuffle == rpc.ShuffleStrategyForward {
			neighbors[edge.SourceOperatorID] = append(neighbors[edge.SourceOperatorID], edge.TargetOperatorID)
			neighbors[edge.TargetOperatorID] = append(neighbors[edge.TargetOperatorID], edge.SourceOperatorID)
		}
	}
	pinned := make(map[string]int32)
	var pending []string
	for _, op := range graph.Operators {
		if op.Type == rpc.OperatorTypeSource || op.Type == rpc.OperatorTypeSink {
			p := op.Parallelism
			if p == 0 {
				p = int32(defaultParallelism)
			}
			pinned[op.OperatorID] = p
			pending = append(pending, op.OperatorID)
		}
	}
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		for _, neighbor := range neighbors[id] {
			if _, seen := pinned[neighbor]; !seen {
				pinned[neighbor] = pinned[id]
				pending = append(pending, neighbor)
			}
		}
	}
	return pinned
}
