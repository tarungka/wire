package coordinator

import (
	"fmt"
	"maps"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// ReplaceJobFromSavepoint installs a compatible-layout replacement in the existing
// job identity. Old tasks are fenced and joined by the normal restart path.
// Failed placement/deployment restores the old graph through rollback handling.
func (c *Coordinator) ReplaceJobFromSavepoint(jobID, savepointID string, parallelism int, config []byte) (*JobMeta, error) {
	return c.replaceJobFromSavepoint(jobID, savepointID, parallelism, config, "")
}

func (c *Coordinator) replaceJobFromSavepoint(jobID, savepointID string, parallelism int, config []byte, requestID string) (*JobMeta, error) {
	config, err := c.resolveStateBackendDefaults(config)
	if err != nil {
		return nil, err
	}
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(config, &graph); err != nil {
		return nil, fmt.Errorf("%w: replacement requires a structured graph", ErrInvalidConfig)
	}
	if err := graph.CheckpointPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := graph.RestartPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	secrets, err := resolveJobSecretReferences(graph)
	if err != nil {
		return nil, err
	}
	installed := false
	defer func() {
		if !installed {
			secrets.clear()
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return nil, ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		return nil, ErrJobNotFound
	}
	if job.Status != JobRunning || job.PauseSavepointID != "" {
		return nil, ErrJobNotRunning
	}
	if c.activeCheckpoints[jobID].ID != 0 {
		return nil, ErrCheckpointInProgress
	}
	sp, err := c.GetSavepoint(jobID, savepointID)
	if err != nil {
		return nil, err
	}
	if sp.Status != SavepointCompleted || sp.CheckpointID == 0 || sp.CheckpointID != job.LatestCheckpoint {
		return nil, fmt.Errorf("%w: replacement requires latest completed savepoint", ErrInvalidTransition)
	}
	next := *job
	next.ReplacementRequestID = requestID
	next.Config = append([]byte(nil), config...)
	next.Parallelism = parallelism
	next.CheckpointPolicy = graph.CheckpointPolicy
	next.RestartPolicy = graph.RestartPolicy
	next.ReplacementCheckpoint = sp.CheckpointID
	next.RescaleCheckpoint = 0
	next.RescaleFailure = ""
	next.RescaleRequested = true
	next.RescaleRollback = &RescaleRollback{TransactionTaskIDs: maps.Clone(job.TransactionTaskIDs), Config: append([]byte(nil), job.Config...), Parallelism: job.Parallelism, Checkpoint: job.LatestCheckpoint}
	sources, err := generateTaskDescriptors(job)
	if err != nil {
		return nil, err
	}
	targets, err := generateTaskDescriptors(&next)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("%w: empty replacement layout", ErrInvalidConfig)
	}
	plan, err := planTaskLayoutRestoreMode(sources[0].NumKeyGroups, sources, targets, true)
	if err != nil {
		return nil, err
	}
	next.TransactionTaskIDs = remapTransactionIdentities(job, plan)
	if err := c.attachCheckpointRestoreLocked(&next, map[string][]rpc.TaskDescriptor{"validation": targets}); err != nil {
		return nil, err
	}
	c.resetStableRecoveryBudget(&next, time.Now())
	next.Status = JobFailing
	next.UpdatedAt = time.Now().UTC()
	if err := c.persistJobLocked(&next); err != nil {
		return nil, err
	}
	*job = next
	c.installJobSecretsLocked(jobID, secrets)
	installed = true
	c.kickScheduler()
	return &next, nil
}
