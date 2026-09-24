package worker

import (
	"context"
	"fmt"
	"sync"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type taskCheckpointRuntime struct {
	sourceExhausted func(context.Context) error
	triggerMu       sync.Mutex
	lastTrigger     uint64
	rescale         []engine.OperatorRescaleState
	restoredID      uint64
	restore         *engine.TaskCheckpoint
	source          bool
	triggers        chan engine.CheckpointTrigger
	decisions       chan engine.ControlMsg
	replicator      engine.CheckpointReplicator
	report          func(context.Context, uint64, uint64, error) error
}

func (w *Worker) prepareTaskCheckpoint(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor) (*taskCheckpointRuntime, func(), error) {
	if desc.CheckpointReplicaAddress == "" && desc.RestoreCheckpoint == nil && desc.RestoreRescale == nil {
		return nil, func() {}, nil
	}
	if w.cfg.CheckpointReplica == nil || desc.EpochID == 0 {
		return nil, nil, fmt.Errorf("checkpoint deployment requires replica configuration and epoch")
	}
	w.mu.RLock()
	handle := w.tasks[taskID]
	w.mu.RUnlock()
	if handle == nil || handle.checkpoint == nil {
		return nil, nil, fmt.Errorf("checkpoint task is no longer assigned")
	}
	runtime := handle.checkpoint
	if desc.RestoreRescale != nil {
		states, err := w.fetchRescaleState(ctx, jobID, taskID, desc)
		if err != nil {
			return nil, nil, err
		}
		runtime.rescale = states
		runtime.restoredID = desc.RestoreRescale.CheckpointID
	}
	if desc.RestoreCheckpoint != nil {
		snapshot, err := w.fetchTaskCheckpoint(ctx, jobID, taskID, desc)
		if err != nil {
			return nil, nil, err
		}
		runtime.restore = snapshot
	}
	if desc.CheckpointReplicaAddress == "" {
		return runtime, func() {}, nil
	}
	maxFailures := 0
	if w.executor.taskConfig != nil {
		maxFailures = w.executor.taskConfig.Checkpoint.MaxConsecutiveFailures
	}
	consecutiveFailures := 0
	replicator := &archiveCheckpointReplicator{jobID: jobID, taskID: taskID, epoch: desc.EpochID, stagingRoot: w.cfg.CheckpointReplica.StagingRoot, client: &reconnectingCheckpointClient{address: desc.CheckpointReplicaAddress, tlsConfig: w.cfg.PeerTLSConfig}}
	runtime.replicator = replicator
	runtime.sourceExhausted = func(ctx context.Context) error {
		w.mu.Lock()
		if w.tasks[taskID] != handle {
			w.mu.Unlock()
			return fmt.Errorf("source attempt replaced")
		}
		handle.status = rpc.TaskStatusFinishing
		w.mu.Unlock()
		response, err := w.client.UpdateTaskStatus(ctx, &rpc.UpdateTaskStatusRequest{WorkerID: w.cfg.WorkerID, JobID: jobID, TaskID: taskID, AttemptID: desc.AttemptID, EpochID: desc.EpochID, Status: rpc.TaskStatusFinishing})
		if err != nil {
			return err
		}
		if !response.Accepted {
			return fmt.Errorf("source completion refused: %s", response.Message)
		}
		return nil
	}
	runtime.report = func(ctx context.Context, id, epoch uint64, uploadErr error) error {
		request := &rpc.AcknowledgeCheckpointRequest{AttemptID: desc.AttemptID, WorkerID: w.cfg.WorkerID, JobID: jobID, TaskID: taskID, CheckpointID: id, EpochID: epoch}
		if uploadErr != nil {
			request.Failure = uploadErr.Error()
		} else {
			request.State = &rpc.StateHandle{TaskID: taskID, Path: desc.CheckpointReplicaAddress, Manifest: replicator.manifest(id)}
		}
		response, err := w.client.AcknowledgeCheckpoint(ctx, request)
		if err == nil && !response.Accepted {
			err = fmt.Errorf("checkpoint report rejected: %s", response.Message)
		}
		if err != nil {
			w.log.Warn().Err(err).Uint64("checkpoint_id", id).Msg("checkpoint report failed")
			if uploadErr == nil {
				uploadErr = err
			}
		}
		if uploadErr == nil {
			consecutiveFailures = 0
		} else {
			consecutiveFailures++
			if maxFailures > 0 && consecutiveFailures >= maxFailures {
				return fmt.Errorf("%w: %d consecutive upload failures", engine.ErrMaxConsecutiveCheckpointFailures, consecutiveFailures)
			}
		}
		return nil
	}
	return runtime, func() {}, nil
}

func (w *Worker) handleCheckpointCommand(command rpc.WorkerCommand) (accepted bool) {
	var request rpc.TriggerCheckpointRequest
	if err := protocol.DecodeMsgPack(command.Data, &request); err != nil {
		w.log.Warn().Err(err).Msg("invalid checkpoint command")
		return
	}
	w.mu.RLock()
	handle := w.tasks[command.TaskID]
	if handle == nil || request.AttemptID != handle.attemptID || handle.jobID != command.JobID || request.JobID != command.JobID || request.CheckpointID == 0 || request.EpochID != handle.epoch || request.EpochID != w.epoch || handle.checkpoint == nil {
		w.mu.RUnlock()
		return
	}
	checkpoint := handle.checkpoint
	accepted = true
	w.mu.RUnlock()
	if command.Type == rpc.CommandTypeTakeSnapshot {
		if !checkpoint.source {
			return
		}
		checkpoint.triggerMu.Lock()
		defer checkpoint.triggerMu.Unlock()
		if request.CheckpointID <= checkpoint.lastTrigger {
			return
		}
		checkpoint.lastTrigger = request.CheckpointID
		select {
		case checkpoint.triggers <- engine.CheckpointTrigger{Final: request.Final, CheckpointID: request.CheckpointID, EpochID: request.EpochID}:
			return
		default:
			// Coalesce queued source triggers. The coordinator permits only
			// one active checkpoint; an older queued identity is obsolete.
			select {
			case <-checkpoint.triggers:
			default:
			}
			select {
			case checkpoint.triggers <- engine.CheckpointTrigger{Final: request.Final, CheckpointID: request.CheckpointID, EpochID: request.EpochID}:
			default:
			}
			return
		}
	} else {
		kind := engine.CtrlAbortCheckpoint
		if command.Type == rpc.CommandTypeCommitCheckpoint {
			kind = engine.CtrlCommitCheckpoint
		}
		// The chain owns transaction state. One abort control atomically
		// rolls back only a matching prepared transaction, or retires an
		// unprepared alignment while preserving its current writes.
		select {
		case checkpoint.decisions <- engine.ControlMsg{Type: kind, CheckpointID: request.CheckpointID, EpochID: request.EpochID}:
			return
		default:
		}
	}
	w.log.Error().Str("task_id", command.TaskID).Msg("checkpoint command mailbox exhausted")
	handle.cancel()
	return
}
