package coordinator

import (
	"context"
	"fmt"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// HandleRegisterWorker is an RPC handler that bridges the rpc.Server to
// the Coordinator's RegisterWorker method.
func (c *Coordinator) HandleRegisterWorker(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var rpcReq rpc.RegisterWorkerRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &rpcReq); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, fmt.Sprintf("decode RegisterWorkerRequest: %v", err))
	}

	// Map RPC request to coordinator domain request.
	coordReq := RegisterWorkerRequest{
		WorkerID:         rpcReq.WorkerID,
		Address:          rpcReq.Address,
		TaskSlotsTotal:   rpcReq.TaskSlotsTotal,
		HighestSeenEpoch: rpcReq.HighestSeenEpoch,
		RunningTasks:     rpcReq.RunningTasks,
	}

	resp, err := c.RegisterWorker(coordReq)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, fmt.Sprintf("register worker: %v", err))
	}

	return &rpc.RegisterWorkerResponse{
		Epoch:         resp.Epoch,
		TasksToCancel: resp.TasksToCancel,
		MissingTasks:  resp.MissingTasks,
	}, nil
}

// HandleHeartbeat is an RPC handler that bridges the rpc.Server to
// the Coordinator's worker heartbeat tracking.
func (c *Coordinator) HandleHeartbeat(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var req rpc.HeartbeatRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &req); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, fmt.Sprintf("decode HeartbeatRequest: %v", err))
	}

	// Update in-memory worker state.
	c.mu.RLock()
	w, ok := c.workers[req.WorkerID]
	c.mu.RUnlock()

	if !ok {
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, fmt.Sprintf("unknown worker: %s", req.WorkerID))
	}

	c.mu.Lock()
	if req.Load != nil {
		w.TaskSlotsAvailable = w.TaskSlotsTotal - int(req.Load.ActiveSlots)
	}
	epoch := c.epoch
	c.mu.Unlock()

	// Drain pending commands for this worker.
	cmds := c.DrainCommands(req.WorkerID)

	return &rpc.HeartbeatResponse{
		Accepted: true,
		EpochID:  epoch,
		Commands: cmds,
	}, nil
}

// HandleWatchCommands is the server-streaming handler that pushes
// WorkerCommand frames to the worker as they're enqueued by the
// scheduler. The worker opens this stream once after RegisterWorker and
// keeps it alive for the lifetime of its session — replacing the old
// pull-on-heartbeat dispatch model.
//
// The handler returns when the stream is closed (worker disconnect or
// session shutdown), which deregisters the channel; subsequent
// EnqueueCommand calls fall back to the heartbeat slice queue until
// the worker reconnects.
func (c *Coordinator) HandleWatchCommands(ctx context.Context, requestID uint64, payload []byte, stream *yamux.Stream) error {
	var req rpc.WatchCommandsRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &req); err != nil {
		return c.writeStreamError(stream, requestID, rpc.ErrCodeSerializationError,
			fmt.Sprintf("decode WatchCommandsRequest: %v", err))
	}
	if req.WorkerID == "" {
		return c.writeStreamError(stream, requestID, rpc.ErrCodeInternalError, "WorkerID is required")
	}

	ch, cleanup := c.RegisterCommandStream(req.WorkerID)
	defer cleanup()

	c.log.Info().
		Str("worker_id", req.WorkerID).
		Uint64("epoch", req.EpochID).
		Msg("worker opened command-watch stream")

	for {
		select {
		case <-ctx.Done():
			return nil
		case cmd, ok := <-ch:
			if !ok {
				// Channel closed — coordinator deregistered (e.g. on
				// shutdown or because a newer stream replaced this one).
				return nil
			}
			payload, err := protocol.EncodeMsgPack(cmd)
			if err != nil {
				c.log.Error().Err(err).Str("worker_id", req.WorkerID).Msg("encode WorkerCommand")
				continue
			}
			if err := rpc.WriteRPCFrame(stream, rpc.RPCFrame{
				MethodID:  rpc.MethodWatchCommands,
				RequestID: requestID,
				Payload:   payload,
			}); err != nil {
				// Worker disconnected or stream errored — exit so
				// cleanup() drains the registration.
				c.log.Debug().Err(err).Str("worker_id", req.WorkerID).Msg("command-watch stream write failed")
				return err
			}
		}
	}
}

func (c *Coordinator) writeStreamError(stream *yamux.Stream, requestID uint64, code rpc.ErrorCode, msg string) error {
	rpcErr := rpc.NewRPCError(code, msg)
	payload, encErr := protocol.EncodeMsgPack(rpcErr)
	if encErr != nil {
		return encErr
	}
	return rpc.WriteRPCFrame(stream, rpc.RPCFrame{
		MethodID:  rpc.MethodError,
		RequestID: requestID,
		Payload:   payload,
	})
}

// HandleUpdateTaskStatus is an RPC handler that processes task status updates
// from workers.
func (c *Coordinator) HandleUpdateTaskStatus(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var req rpc.UpdateTaskStatusRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &req); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, fmt.Sprintf("decode UpdateTaskStatusRequest: %v", err))
	}

	c.mu.Lock()
	c.taskStatuses[req.TaskID] = req.Status
	job, jobExists := c.jobs[req.JobID]
	c.mu.Unlock()

	c.log.Info().
		Str("task_id", req.TaskID).
		Str("job_id", req.JobID).
		Str("status", req.Status.String()).
		Msg("task status updated")

	if !jobExists {
		return &rpc.UpdateTaskStatusResponse{Accepted: true}, nil
	}

	// Check for job-level transitions based on task status.
	switch req.Status {
	case rpc.TaskStatusRunning:
		c.mu.RLock()
		allRunning := c.allTasksInStatus(req.JobID, rpc.TaskStatusRunning)
		currentStatus := job.Status
		c.mu.RUnlock()

		if allRunning && currentStatus == JobDeploying {
			if err := c.transitionJob(job, JobRunning); err != nil {
				c.log.Warn().Err(err).Str("job_id", req.JobID).Msg("failed to transition job to RUNNING")
			} else {
				c.log.Info().Str("job_id", req.JobID).Msg("all tasks running, job is RUNNING")
			}
		}

	case rpc.TaskStatusFailed:
		c.mu.RLock()
		currentStatus := job.Status
		c.mu.RUnlock()

		if currentStatus == JobDeploying || currentStatus == JobRunning {
			if err := c.transitionJob(job, JobFailing); err != nil {
				c.log.Warn().Err(err).Str("job_id", req.JobID).Msg("failed to transition job to FAILING")
			} else {
				c.log.Info().Str("job_id", req.JobID).Msg("task failed, job is FAILING")
			}
		}

	case rpc.TaskStatusFinished:
		c.mu.RLock()
		allFinished := c.allTasksInStatus(req.JobID, rpc.TaskStatusFinished)
		currentStatus := job.Status
		c.mu.RUnlock()

		// JobRunning → JobFinishing → JobFinished. The Finishing stage is a
		// synchronous pass-through for Phase 1 since there is no coordinated
		// flush/commit step yet; the 2PC sink path in Phase 4 will linger
		// here until PreCommit/Commit acks arrive.
		if allFinished && currentStatus == JobRunning {
			if err := c.transitionJob(job, JobFinishing); err != nil {
				c.log.Warn().Err(err).Str("job_id", req.JobID).Msg("failed to transition job to FINISHING")
				break
			}
			if err := c.transitionJob(job, JobFinished); err != nil {
				c.log.Warn().Err(err).Str("job_id", req.JobID).Msg("failed to transition job to FINISHED")
			} else {
				c.log.Info().Str("job_id", req.JobID).Msg("all tasks finished, job is FINISHED")
			}
		}
	}

	return &rpc.UpdateTaskStatusResponse{Accepted: true}, nil
}
