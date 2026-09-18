package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/tarungka/wire/internal/engine"

	"github.com/tarungka/wire/internal/checkpointpolicy"
	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/observability"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// TriggerCheckpoint persists the checkpoint boundary and its exact task set
// before dispatching commands. Completion requires durable receipts from every
// task in that set; command delivery alone does not complete the checkpoint.
func (c *Coordinator) TriggerCheckpoint(jobID string) (*CheckpointMeta, error) {
	return c.triggerCheckpoint(jobID, "")
}

func (c *Coordinator) triggerCheckpoint(jobID, savepointID string) (*CheckpointMeta, error) {
	return c.triggerCheckpointBoundary(jobID, savepointID, false)
}

func (c *Coordinator) triggerCheckpointBoundary(jobID, savepointID string, final bool) (*CheckpointMeta, error) {
	c.mu.Lock()
	if c.state != StateLeader || !c.recovered {
		c.mu.Unlock()
		return nil, ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		c.mu.Unlock()
		return nil, ErrJobNotFound
	}
	if job.Status != JobRunning {
		c.mu.Unlock()
		return nil, ErrJobNotRunning
	}
	if !final && savepointID == "" && c.config.CheckpointMinPause > 0 && !job.LastCheckpointCompletion.IsZero() && time.Since(job.LastCheckpointCompletion) < c.config.CheckpointMinPause {
		c.mu.Unlock()
		return nil, ErrCheckpointMinPause
	}
	data, err := c.store.Get(JobAssignmentsKey(jobID))
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	var assignment TaskAssignmentMap
	if err := protocol.DecodeMsgPack(data, &assignment); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if assignment.JobID != jobID || len(assignment.Assignments) == 0 {
		c.mu.Unlock()
		return nil, errors.New("checkpoint has no task assignments")
	}
	if final && !c.sourcesExhaustedLocked(assignment) {
		c.mu.Unlock()
		return nil, errFinalCheckpointNotReady
	}
	for taskID := range assignment.Assignments {
		if assignment.Replicas[taskID] == "" {
			c.mu.Unlock()
			return nil, fmt.Errorf("%w: task %s has no assigned replica", ErrCheckpointUnavailable, taskID)
		}
	}
	var highest uint64
	var scanErr error
	err = c.store.PrefixScan([]byte(fmt.Sprintf("jobs/%s/checkpoints/", jobID)), func(key, value []byte) bool {
		if bytes.Equal(key, LatestCheckpointKey(jobID)) || bytes.HasSuffix(key, []byte("/metadata.json")) {
			return true
		}
		var previous CheckpointMeta
		if err := protocol.DecodeMsgPack(value, &previous); err != nil {
			scanErr = err
			return false
		}
		if previous.Final && previous.AttemptID == assignment.AttemptID && previous.Status == CheckpointCompleted {
			scanErr = errFinalCheckpointNotReady
			return false
		}
		if previous.Status == CheckpointTriggered || previous.Status == CheckpointInProgress {
			scanErr = ErrCheckpointInProgress
			return false
		}
		if previous.ID > highest {
			highest = previous.ID
		}
		return true
	})
	if err != nil || scanErr != nil {
		c.mu.Unlock()
		return nil, errors.Join(err, scanErr)
	}
	if highest == math.MaxUint64 {
		c.mu.Unlock()
		return nil, errors.New("checkpoint IDs exhausted")
	}
	count := keygroup.DefaultNumKeyGroups
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(job.Config, &graph); err == nil {
		count, err = validateGraphKeyGroups(graph, max(1, job.Parallelism))
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}
	checkpoint := &CheckpointMeta{Final: final, AttemptID: assignment.AttemptID, ManifestVersion: 1, TaskDescriptors: assignment.TaskDescriptors, NumKeyGroups: count, SavepointID: savepointID, ID: highest + 1, EpochID: c.epoch, JobID: jobID, Status: CheckpointInProgress, Timestamp: time.Now().UTC(), Tasks: assignment.Assignments, Replicas: assignment.Replicas}
	encoded, err := protocol.EncodeMsgPack(checkpoint)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	kind := rpc.CheckpointTypePeriodic
	if savepointID != "" {
		kind = rpc.CheckpointTypeSavepoint
	}
	trigger, err := protocol.EncodeMsgPack(rpc.TriggerCheckpointRequest{Final: final, AttemptID: checkpoint.AttemptID, JobID: jobID, CheckpointID: checkpoint.ID, EpochID: checkpoint.EpochID, Type: kind, Timestamp: checkpoint.Timestamp.UnixMilli()})
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	nextJob := *job
	if savepointID == "" {
		nextJob.CheckpointAttempts++
	}
	jobData, err := protocol.EncodeMsgPack(nextJob)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	batch := []KVPair{{Key: JobMetaKey(jobID), Value: jobData}, {Key: CheckpointKey(jobID, checkpoint.ID), Value: encoded}}
	if savepointID != "" {
		savepoint := SavepointMeta{NumKeyGroups: count, ID: savepointID, JobID: jobID, CheckpointID: checkpoint.ID, EpochID: checkpoint.EpochID, Status: SavepointInProgress, TriggerTime: checkpoint.Timestamp}
		raw, err := protocol.EncodeMsgPack(savepoint)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		batch = append(batch, KVPair{Key: SavepointKey(jobID, savepointID), Value: raw})
	}
	if err := c.store.WriteBatch(batch); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	job.CheckpointAttempts = nextJob.CheckpointAttempts
	if c.activeCheckpoints == nil {
		c.activeCheckpoints = make(map[string]CheckpointMeta)
	}
	c.activeCheckpoints[jobID] = *checkpoint
	c.mu.Unlock()
	sent := make(map[string]bool)
	for taskID, workerID := range assignment.Assignments {
		c.mu.RLock()
		worker := c.workers[workerID]
		var peer *rpc.Client
		reserved := worker != nil && worker.SupportsReservations
		if reserved {
			peer = worker.RPCClient
		}
		c.mu.RUnlock()
		if !reserved {
			c.EnqueueCommand(workerID, rpc.WorkerCommand{Type: rpc.CommandTypeTakeSnapshot, JobID: jobID, TaskID: taskID, Data: trigger})
			continue
		}
		if sent[workerID] {
			continue
		}
		sent[workerID] = true
		var request rpc.TriggerCheckpointRequest
		if err := protocol.DecodeMsgPack(trigger, &request); err != nil {
			return nil, err
		}
		var triggerErr error
		if peer == nil {
			triggerErr = errors.New("checkpoint worker RPC disconnected")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), rpc.DefaultTriggerCheckpointTimeout)
			response, err := peer.TriggerCheckpoint(ctx, &request)
			cancel()
			triggerErr = err
			if err == nil && !response.Accepted {
				triggerErr = errors.New(response.Message)
			}
		}
		if triggerErr != nil {
			if err := c.abortCheckpoint(jobID, checkpoint.ID, checkpoint.EpochID, triggerErr.Error(), false); err != nil {
				return nil, err
			}
			return nil, triggerErr
		}
	}
	return checkpoint, nil
}

// AbortCheckpoint fences stale failures and persists the terminal decision
// before notifying the exact task assignments captured at the boundary.
func (c *Coordinator) AbortCheckpoint(jobID string, id, epoch uint64) error {
	return c.abortCheckpoint(jobID, id, epoch, "", false)
}

func (c *Coordinator) abortCheckpoint(jobID string, id, epoch uint64, failure string, timedOut bool) error {
	c.mu.Lock()
	if c.state != StateLeader || !c.recovered {
		c.mu.Unlock()
		return ErrNotLeader
	}
	if epoch != c.epoch {
		c.mu.Unlock()
		return ErrStaleEpoch
	}
	data, err := c.store.Get(CheckpointKey(jobID, id))
	if err != nil {
		c.mu.Unlock()
		return err
	}
	var checkpoint CheckpointMeta
	if err := protocol.DecodeMsgPack(data, &checkpoint); err != nil {
		c.mu.Unlock()
		return err
	}
	if checkpoint.JobID != jobID || checkpoint.ID != id || checkpoint.EpochID != epoch {
		c.mu.Unlock()
		return ErrStaleEpoch
	}
	if checkpoint.Status != CheckpointInProgress && checkpoint.Status != CheckpointTriggered && checkpoint.Status != CheckpointAborted {
		c.mu.Unlock()
		return errors.New("checkpoint already has a different terminal decision")
	}
	alreadyAborted := checkpoint.Status == CheckpointAborted
	checkpoint.Status = CheckpointAborted
	encoded, err := protocol.EncodeMsgPack(checkpoint)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	command, err := protocol.EncodeMsgPack(rpc.TriggerCheckpointRequest{AttemptID: checkpoint.AttemptID, JobID: jobID, CheckpointID: id, EpochID: epoch})
	if err != nil {
		c.mu.Unlock()
		return err
	}
	batch := []KVPair{{Key: CheckpointKey(jobID, id), Value: encoded}}
	var savepointWrite *KVPair
	if !alreadyAborted {
		savepointWrite, err = c.savepointDecisionLocked(checkpoint)
	}
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if savepointWrite != nil {
		batch = append(batch, *savepointWrite)
	}
	var failedJob *JobMeta
	if !alreadyAborted && checkpoint.SavepointID == "" && (failure != "" || checkpoint.Final) {
		if job := c.jobs[jobID]; job != nil {
			next := *job
			next.CheckpointOutcomes = checkpointpolicy.Record(next.CheckpointOutcomes, true)
			next.CheckpointFailures++
			next.ConsecutiveCheckpointFailures++
			next.CheckpointFailure = failure
			threshold := c.config.CheckpointMaxConsecutiveFailures > 0 && next.ConsecutiveCheckpointFailures >= c.config.CheckpointMaxConsecutiveFailures
			rateExceeded := checkpointpolicy.Exceeded(next.CheckpointOutcomes, c.config.CheckpointTolerableFailureRate)
			if (threshold || rateExceeded || checkpoint.Final) && next.Status == JobRunning {
				c.resetStableRecoveryBudget(&next, time.Now())
				next.Status = JobFailing
				next.UpdatedAt = time.Now().UTC()
			}
			jobData, err := protocol.EncodeMsgPack(next)
			if err != nil {
				c.mu.Unlock()
				return err
			}
			batch = append(batch, KVPair{Key: JobMetaKey(jobID), Value: jobData})
			failedJob = &next
		}
	}
	if err := c.store.WriteBatch(batch); err != nil {
		c.mu.Unlock()
		return err
	}
	if active, ok := c.activeCheckpoints[jobID]; ok && active.ID == id && active.EpochID == epoch {
		delete(c.activeCheckpoints, jobID)
	}
	if failedJob != nil {
		job := c.jobs[jobID]
		job.CheckpointOutcomes = failedJob.CheckpointOutcomes
		job.CheckpointFailures = failedJob.CheckpointFailures
		job.ConsecutiveCheckpointFailures = failedJob.ConsecutiveCheckpointFailures
		job.CheckpointFailure = failedJob.CheckpointFailure
		job.Status = failedJob.Status
		job.UpdatedAt = failedJob.UpdatedAt
		job.RecoveryAttempts = failedJob.RecoveryAttempts
	}
	if timedOut && !alreadyAborted {
		observability.RecordCheckpointTimeout(jobID)
	}
	// Publish abort commands before the scheduler can cancel a failed job.
	// Retried decisions are idempotent and never charge failure policy twice.
	for taskID, workerID := range checkpoint.Tasks {
		c.enqueueCommandLocked(workerID, rpc.WorkerCommand{Type: rpc.CommandTypeAbortCheckpoint, JobID: jobID, TaskID: taskID, Data: command})
	}
	c.mu.Unlock()
	return nil
}

// AcknowledgeCheckpoint records a worker's durable state location. Workers must
// report only after the replica receipt; the coordinator fences assignment and
// epoch and atomically commits metadata once every captured task has reported.
func (c *Coordinator) AcknowledgeCheckpoint(request rpc.AcknowledgeCheckpointRequest) error {
	decision, err := protocol.EncodeMsgPack(rpc.TriggerCheckpointRequest{AttemptID: request.AttemptID, JobID: request.JobID, CheckpointID: request.CheckpointID, EpochID: request.EpochID})
	if err != nil {
		return err
	}
	var notify map[string]string
	c.mu.Lock()
	defer func() {
		c.mu.Unlock()
		for taskID, workerID := range notify {
			c.EnqueueCommand(workerID, rpc.WorkerCommand{Type: rpc.CommandTypeCommitCheckpoint, JobID: request.JobID, TaskID: taskID, Data: decision})
		}
	}()
	if c.state != StateLeader || !c.recovered {
		return ErrNotLeader
	}
	if request.EpochID != c.epoch {
		return ErrStaleEpoch
	}
	data, err := c.store.Get(CheckpointKey(request.JobID, request.CheckpointID))
	if err != nil {
		return err
	}
	var checkpoint CheckpointMeta
	if err := protocol.DecodeMsgPack(data, &checkpoint); err != nil {
		return err
	}
	if checkpoint.ID != request.CheckpointID || checkpoint.JobID != request.JobID || checkpoint.EpochID != request.EpochID {
		return ErrStaleEpoch
	}
	worker, ok := checkpoint.Tasks[request.TaskID]
	if !ok || worker == "" || worker != request.WorkerID || request.AttemptID != checkpoint.AttemptID {
		return errors.New("checkpoint acknowledgement does not match task assignment")
	}
	if request.State == nil || request.State.TaskID != request.TaskID || request.State.Path == "" {
		return errors.New("checkpoint acknowledgement requires durable state location")
	}
	if expected := checkpoint.Replicas[request.TaskID]; expected == "" || request.State.Path != expected {
		return errors.New("checkpoint acknowledgement does not match replica assignment")
	}
	if checkpoint.Status != CheckpointInProgress && checkpoint.Status != CheckpointCompleted {
		return errors.New("checkpoint no longer accepts acknowledgements")
	}
	if checkpoint.ManifestVersion != 0 {
		if len(request.State.Manifest) == 0 {
			return errors.New("checkpoint acknowledgement requires task manifest")
		}
		var task engine.TaskMeta
		if err := json.Unmarshal(request.State.Manifest, &task); err != nil {
			return err
		}
		if task.TaskID != request.TaskID {
			return errors.New("task manifest identity mismatch")
		}
		if checkpoint.TaskManifests == nil {
			checkpoint.TaskManifests = make(map[string][]byte)
		}
		if prior, ok := checkpoint.TaskManifests[request.TaskID]; ok && !bytes.Equal(prior, request.State.Manifest) {
			return errors.New("conflicting task manifest")
		}
		checkpoint.TaskManifests[request.TaskID] = append([]byte(nil), request.State.Manifest...)
	}
	if checkpoint.StatePaths == nil {
		checkpoint.StatePaths = make(map[string]string)
	}
	if previous, exists := checkpoint.StatePaths[request.TaskID]; exists && previous != request.State.Path {
		return errors.New("conflicting checkpoint acknowledgement")
	}
	checkpoint.StatePaths[request.TaskID] = request.State.Path
	if checkpoint.Status == CheckpointCompleted {
		notify = checkpoint.Tasks
		return nil
	}
	complete := len(checkpoint.StatePaths) == len(checkpoint.Tasks)
	if complete {
		checkpoint.Status = CheckpointCompleted
	}
	encoded, err := protocol.EncodeMsgPack(checkpoint)
	if err != nil {
		return err
	}
	batch := []KVPair{{Key: CheckpointKey(request.JobID, request.CheckpointID), Value: encoded}}
	if complete && checkpoint.ManifestVersion != 0 {
		inventory := make(map[string]engine.TaskMeta)
		for id, raw := range checkpoint.TaskManifests {
			var task engine.TaskMeta
			if err := json.Unmarshal(raw, &task); err != nil {
				return err
			}
			inventory[id] = task
		}
		manifest, err := checkpointManifest(c.jobs[request.JobID], checkpoint, inventory, time.Now().UTC())
		if err != nil {
			return err
		}
		raw, err := engine.MarshalCheckpointMetadata(manifest)
		if err != nil {
			return err
		}
		batch = append(batch, KVPair{Key: CheckpointManifestKey(request.JobID, request.CheckpointID), Value: raw})
	}
	var next JobMeta
	if complete {
		job := c.jobs[request.JobID]
		if job == nil {
			return ErrJobNotFound
		}
		next = *job
		next.LatestCheckpoint = checkpoint.ID
		next.LastCheckpointCompletion = time.Now().UTC()
		if checkpoint.SavepointID == "" {
			next.CheckpointOutcomes = checkpointpolicy.Record(next.CheckpointOutcomes, false)
			next.ConsecutiveCheckpointFailures = 0
			next.CheckpointFailure = ""
		}
		jobData, err := protocol.EncodeMsgPack(next)
		if err != nil {
			return err
		}
		batch = append(batch, KVPair{Key: JobMetaKey(request.JobID), Value: jobData})
		savepointWrite, err := c.savepointDecisionLocked(checkpoint)
		if err != nil {
			return err
		}
		if savepointWrite != nil {
			batch = append(batch, *savepointWrite)
		}
	}
	if err := c.store.WriteBatch(batch); err != nil {
		return err
	}
	if complete {
		job := c.jobs[request.JobID]
		job.LatestCheckpoint = next.LatestCheckpoint
		job.LastCheckpointCompletion = next.LastCheckpointCompletion
		job.CheckpointOutcomes = next.CheckpointOutcomes
		job.ConsecutiveCheckpointFailures = next.ConsecutiveCheckpointFailures
		job.CheckpointFailure = next.CheckpointFailure
		delete(c.activeCheckpoints, request.JobID)
		notify = checkpoint.Tasks
	}
	return nil
}

// ReportCheckpointFailure validates the reporting task before applying the
// epoch-fenced abort. A stale report cannot affect a new checkpoint execution.
func (c *Coordinator) ReportCheckpointFailure(request rpc.AcknowledgeCheckpointRequest) error {
	if request.Failure == "" {
		return errors.New("checkpoint failure reason is required")
	}
	c.mu.RLock()
	if c.state != StateLeader || !c.recovered {
		c.mu.RUnlock()
		return ErrNotLeader
	}
	if request.EpochID != c.epoch {
		c.mu.RUnlock()
		return ErrStaleEpoch
	}
	data, err := c.store.Get(CheckpointKey(request.JobID, request.CheckpointID))
	c.mu.RUnlock()
	if err != nil {
		return err
	}
	var checkpoint CheckpointMeta
	if err := protocol.DecodeMsgPack(data, &checkpoint); err != nil {
		return err
	}
	worker, ok := checkpoint.Tasks[request.TaskID]
	if !ok || worker == "" || worker != request.WorkerID || request.AttemptID != checkpoint.AttemptID {
		return errors.New("checkpoint failure does not match task assignment")
	}
	return c.abortCheckpoint(request.JobID, request.CheckpointID, request.EpochID, request.Failure, false)
}

// expireCheckpoints shares the coordinator maintenance loop instead of adding
// one timer goroutine per checkpoint. Abort rechecks identity after the lock is
// released, so a concurrent completion cannot be overwritten by a timeout.
func (c *Coordinator) expireCheckpoints(now time.Time) {
	c.mu.Lock()
	var expired []CheckpointMeta
	for jobID, checkpoint := range c.activeCheckpoints {
		if checkpoint.EpochID != c.epoch {
			delete(c.activeCheckpoints, jobID)
			continue
		}
		if now.Sub(checkpoint.Timestamp) >= c.config.CheckpointTimeout {
			expired = append(expired, checkpoint)
		}
	}
	c.mu.Unlock()
	for _, checkpoint := range expired {
		if err := c.abortCheckpoint(checkpoint.JobID, checkpoint.ID, checkpoint.EpochID, "checkpoint timed out", true); err != nil {
			c.log.Warn().Err(err).Str("job_id", checkpoint.JobID).Uint64("checkpoint", checkpoint.ID).Msg("checkpoint timeout abort failed")
		}
	}
}
