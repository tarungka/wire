package coordinator

import (
	"context"
	"fmt"

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
	}

	return &rpc.UpdateTaskStatusResponse{Accepted: true}, nil
}
