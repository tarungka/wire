package coordinator

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// HandleRegisterWorker is an RPC handler that bridges the rpc.Server to
// the Coordinator's RegisterWorker method.
func (c *Coordinator) HandleRegisterWorker(ctx context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var rpcReq rpc.RegisterWorkerRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &rpcReq); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, fmt.Sprintf("decode RegisterWorkerRequest: %v", err))
	}

	// Map RPC request to coordinator domain request.
	coordReq := RegisterWorkerRequest{
		SupportsReservations: rpcReq.SupportsReservations,
		CheckpointAddress:    rpcReq.CheckpointAddress,
		WorkerID:             rpcReq.WorkerID,
		Address:              rpcReq.Address,
		TaskSlotsTotal:       rpcReq.TaskSlotsTotal,
		HighestSeenEpoch:     rpcReq.HighestSeenEpoch,
		RunningTasks:         rpcReq.RunningTasks,
	}

	peer, done := rpc.SessionPeer(ctx)
	resp, err := c.registerWorker(coordReq, peer, done)
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
func (c *Coordinator) HandleHeartbeat(ctx context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var req rpc.HeartbeatRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &req); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, fmt.Sprintf("decode HeartbeatRequest: %v", err))
	}

	c.mu.Lock()
	epoch := c.epoch
	if !c.readyLocked() || req.EpochID != epoch {
		c.mu.Unlock()
		return &rpc.HeartbeatResponse{Accepted: false, EpochID: epoch}, nil
	}
	w, ok := c.workers[req.WorkerID]
	if !ok {
		c.mu.Unlock()
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, fmt.Sprintf("unknown worker: %s", req.WorkerID))
	}
	if peer, _ := rpc.SessionPeer(ctx); peer != nil && w.RPCClient != peer {
		c.mu.Unlock()
		return &rpc.HeartbeatResponse{Accepted: false, EpochID: epoch}, nil
	}
	if w.Removed || w.Lost || w.LastHeartbeat.IsZero() || time.Since(w.LastHeartbeat) >= c.config.WorkerTimeout {
		c.mu.Unlock()
		c.expireTaskWorkers()
		c.kickScheduler()
		return &rpc.HeartbeatResponse{Accepted: false, EpochID: epoch}, nil
	}
	w.LastHeartbeat = time.Now()
	w.Resources = req.Resources
	w.TaskReports = req.Tasks
	freed := false
	if req.Load != nil {
		available := max(0, min(w.TaskSlotsTotal, w.TaskSlotsTotal-int(req.Load.ActiveSlots)))
		freed = available > w.TaskSlotsAvailable
		w.TaskSlotsAvailable = available
	}
	c.mu.Unlock()
	if freed {
		// Covers releases a rejected terminal status could not report, such as
		// a task ending after its job became terminal.
		c.kickScheduler()
	}

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

	if req.Status <= rpc.TaskStatusUnknown || req.Status > rpc.TaskStatusFinished {
		return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "unknown task status")
	}
	c.mu.Lock()
	denied := &rpc.UpdateTaskStatusResponse{Accepted: false, Message: "task status does not match active assignment"}
	if !c.readyLocked() || req.EpochID != c.epoch || req.WorkerID == "" {
		c.mu.Unlock()
		return denied, nil
	}
	job, jobExists := c.jobs[req.JobID]
	if !jobExists || job.Status.IsTerminal() {
		c.mu.Unlock()
		return denied, nil
	}
	assignmentData, err := c.store.Get(JobAssignmentsKey(req.JobID))
	if err != nil {
		c.mu.Unlock()
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, err.Error())
	}
	var assignment TaskAssignmentMap
	if err := protocol.DecodeMsgPack(assignmentData, &assignment); err != nil || assignment.JobID != req.JobID || assignment.Assignments[req.TaskID] != req.WorkerID || assignment.AttemptID != req.AttemptID {
		c.mu.Unlock()
		return denied, nil
	}
	// Retries may arrive after a later status on a separate stream. Never
	// regress an attempt from terminal back to running/deploying.
	previous, known := c.taskStatuses[req.TaskID]
	if known {
		terminal := previous == rpc.TaskStatusFinished || previous == rpc.TaskStatusFailed || previous == rpc.TaskStatusCanceled
		if terminal && previous != req.Status {
			c.mu.Unlock()
			return &rpc.UpdateTaskStatusResponse{Accepted: true, Message: "terminal task status retained"}, nil
		}
		if (previous == rpc.TaskStatusRunning && req.Status == rpc.TaskStatusDeploying) || (previous == rpc.TaskStatusFinishing && (req.Status == rpc.TaskStatusRunning || req.Status == rpc.TaskStatusDeploying)) {
			c.mu.Unlock()
			return &rpc.UpdateTaskStatusResponse{Accepted: true, Message: "newer task status retained"}, nil
		}
	}
	if req.Status == rpc.TaskStatusFailed && req.Failure != nil && req.Failure.ErrorClass == "checkpoint_unavailable" {
		if restore, ok := assignment.RestoreCheckpoints[req.TaskID]; ok {
			sourceJobID := req.JobID
			if restore.SourceJobID != "" {
				sourceJobID = restore.SourceJobID
			}
			raw, err := c.store.Get(CheckpointKey(sourceJobID, restore.CheckpointID))
			var cp CheckpointMeta
			if err == nil {
				err = protocol.DecodeMsgPack(raw, &cp)
			}
			if err == nil && cp.JobID == sourceJobID && cp.EpochID == restore.EpochID {
				cp.InvalidReason = req.Failure.ErrorMessage
				raw, err = protocol.EncodeMsgPack(cp)
				if err == nil {
					// Refund candidate validation once per deployment, atomically with
					// invalidation. Duplicate reports and coordinator restarts cannot
					// refund unrelated execution failures.
					next := *job
					if assignment.RecoveryAttemptCharged && next.RecoveryAttempts > 0 {
						next.RecoveryAttempts--
					}
					assignment.RecoveryAttemptCharged = false
					var jobRaw, assignmentRaw []byte
					jobRaw, err = protocol.EncodeMsgPack(&next)
					if err == nil {
						assignmentRaw, err = protocol.EncodeMsgPack(&assignment)
					}
					if err == nil {
						err = c.store.WriteBatch([]KVPair{
							{Key: CheckpointKey(sourceJobID, cp.ID), Value: raw},
							{Key: JobMetaKey(job.ID), Value: jobRaw},
							{Key: JobAssignmentsKey(job.ID), Value: assignmentRaw},
						})
					}
					if err == nil {
						job.RecoveryAttempts = next.RecoveryAttempts
					}
				}
			}
			if err != nil {
				c.mu.Unlock()
				return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, err.Error())
			}
		}
	}
	c.taskStatuses[req.TaskID] = req.Status
	released := false
	switch req.Status {
	case rpc.TaskStatusFinished, rpc.TaskStatusFailed, rpc.TaskStatusCanceled:
		released = c.releaseTaskSlotLocked(req.WorkerID, req.TaskID)
	}
	c.mu.Unlock()
	terminalChanged := (!known || previous != req.Status) && (req.Status == rpc.TaskStatusCanceled || req.Status == rpc.TaskStatusFailed || req.Status == rpc.TaskStatusFinished)
	if released || terminalChanged {
		// Wake the scheduler after the job-level transition below, so a queued
		// or restarting job can use the slot without waiting for a heartbeat.
		defer c.kickScheduler()
	}

	c.log.Info().
		Str("task_id", req.TaskID).
		Str("job_id", req.JobID).
		Str("status", req.Status.String()).
		Msg("task status updated")

	// Check for job-level transitions based on task status.
	switch req.Status {
	case rpc.TaskStatusRunning, rpc.TaskStatusFinishing:
		if req.Status == rpc.TaskStatusFinishing {
			defer c.kickScheduler()
		}
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

	case rpc.TaskStatusFailed, rpc.TaskStatusCanceled:
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

// HandleAcknowledgeCheckpoint accepts upload reports only through the persisted
// checkpoint state machine; a decoded RPC alone never marks a task durable.
func (c *Coordinator) HandleAcknowledgeCheckpoint(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var request rpc.AcknowledgeCheckpointRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &request); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, fmt.Sprintf("decode checkpoint acknowledgement: %v", err))
	}
	var reportErr error
	if request.Failure != "" {
		reportErr = c.ReportCheckpointFailure(request)
	} else {
		reportErr = c.AcknowledgeCheckpoint(request)
	}
	if reportErr != nil {
		return &rpc.AcknowledgeCheckpointResponse{Accepted: false, Message: reportErr.Error()}, nil
	}
	return &rpc.AcknowledgeCheckpointResponse{Accepted: true}, nil
}
