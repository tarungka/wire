package worker

import (
	"context"
	"fmt"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

type taskCheckpointRuntime struct {
	source     bool
	triggers   chan engine.CheckpointTrigger
	decisions  chan engine.ControlMsg
	replicator engine.CheckpointReplicator
	report     func(context.Context, uint64, uint64, error) error
}

func (w *Worker) prepareTaskCheckpoint(jobID, taskID string, desc rpc.TaskDescriptor) (*taskCheckpointRuntime, func(), error) {
	if desc.CheckpointReplicaAddress == "" {
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
	session, err := transport.NewClientSession(desc.CheckpointReplicaAddress, transport.DefaultConfig())
	if err != nil {
		return nil, nil, err
	}
	runtime := handle.checkpoint
	runtime.replicator = &archiveCheckpointReplicator{jobID: jobID, taskID: taskID, epoch: desc.EpochID, stagingRoot: w.cfg.CheckpointReplica.StagingRoot, client: rpc.NewClient(session.YamuxSession(), rpc.DefaultConfig())}
	runtime.report = func(ctx context.Context, id, epoch uint64, uploadErr error) error {
		request := &rpc.AcknowledgeCheckpointRequest{WorkerID: w.cfg.WorkerID, JobID: jobID, TaskID: taskID, CheckpointID: id, EpochID: epoch}
		if uploadErr != nil {
			request.Failure = uploadErr.Error()
		} else {
			request.State = &rpc.StateHandle{TaskID: taskID, Path: desc.CheckpointReplicaAddress}
		}
		response, err := w.client.AcknowledgeCheckpoint(ctx, request)
		if err != nil {
			return err
		}
		if !response.Accepted {
			return fmt.Errorf("checkpoint report rejected: %s", response.Message)
		}
		return nil
	}
	return runtime, func() { _ = session.Close() }, nil
}

func (w *Worker) handleCheckpointCommand(command rpc.WorkerCommand) {
	var request rpc.TriggerCheckpointRequest
	if err := protocol.DecodeMsgPack(command.Data, &request); err != nil {
		w.log.Warn().Err(err).Msg("invalid checkpoint command")
		return
	}
	w.mu.RLock()
	handle := w.tasks[command.TaskID]
	if handle == nil || handle.jobID != command.JobID || request.JobID != command.JobID || request.CheckpointID == 0 || request.EpochID != handle.epoch || request.EpochID != w.epoch || handle.checkpoint == nil {
		w.mu.RUnlock()
		return
	}
	checkpoint := handle.checkpoint
	w.mu.RUnlock()
	if command.Type == rpc.CommandTypeTakeSnapshot {
		if !checkpoint.source {
			return
		}
		select {
		case checkpoint.triggers <- engine.CheckpointTrigger{CheckpointID: request.CheckpointID, EpochID: request.EpochID}:
			return
		default:
		}
	} else {
		kind := engine.CtrlAbortCheckpoint
		if command.Type == rpc.CommandTypeCommitCheckpoint {
			kind = engine.CtrlCommitCheckpoint
		} else {
			// Roll back prepared sinks before releasing aligned records.
			select {
			case checkpoint.decisions <- engine.ControlMsg{Type: engine.CtrlAbortTransaction, CheckpointID: request.CheckpointID, EpochID: request.EpochID}:
			default:
				w.log.Error().Str("task_id", command.TaskID).Msg("checkpoint command mailbox exhausted")
				handle.cancel()
				return
			}
		}
		select {
		case checkpoint.decisions <- engine.ControlMsg{Type: kind, CheckpointID: request.CheckpointID, EpochID: request.EpochID}:
			return
		default:
		}
	}
	w.log.Error().Str("task_id", command.TaskID).Msg("checkpoint command mailbox exhausted")
	handle.cancel()
}
