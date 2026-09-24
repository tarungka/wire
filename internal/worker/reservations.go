package worker

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type slotReservation struct {
	jobID   string
	epoch   uint64
	slots   int
	expires time.Time
}

func (w *Worker) availableSlotsLocked(now time.Time) int {
	used := len(w.tasks)
	for id, r := range w.reservations {
		if !now.Before(r.expires) {
			delete(w.reservations, id)
			continue
		}
		used += r.slots
	}
	return max(0, w.cfg.TaskSlots-used)
}

func (w *Worker) handleRequestTaskSlots(ctx context.Context, _ uint64, payload []byte) (result any, rpcErr *rpc.RPCError) {
	var req rpc.RequestTaskSlotsRequest
	if err := protocol.DecodeMsgPack(payload, &req); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
	}
	if req.RequiredSlots < 0 || req.ReservationTimeoutMs < 0 || req.ReservationTimeoutMs > 300000 || req.MemoryMB < 0 {
		return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "invalid reservation bounds")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	defer func() {
		if response, ok := result.(*rpc.RequestTaskSlotsResponse); ok {
			response.Resource = &rpc.WorkerResourceInfo{TotalSlots: int32(w.cfg.TaskSlots), UsedSlots: int32(w.cfg.TaskSlots - w.availableSlotsLocked(time.Now()))}
		}
	}()
	if ctx.Err() != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeTimeout, ctx.Err().Error())
	}
	if w.stopping {
		return nil, rpc.NewRPCError(rpc.ErrCodeWorkerShuttingDown, "worker stopping")
	}
	if req.EpochID != w.epoch {
		return nil, rpc.NewRPCError(rpc.ErrCodeStaleEpoch, "reservation epoch mismatch")
	}
	now := time.Now()
	available := w.availableSlotsLocked(now)
	if w.reservations == nil {
		w.reservations = make(map[string]*slotReservation)
	}
	if req.Release {
		if r := w.reservations[req.ReservationID]; r != nil && r.jobID == req.JobID && r.epoch == req.EpochID {
			delete(w.reservations, req.ReservationID)
		}
		return &rpc.RequestTaskSlotsResponse{AvailableSlots: int32(w.availableSlotsLocked(now))}, nil
	}
	if req.RequiredSlots == 0 {
		return &rpc.RequestTaskSlotsResponse{AvailableSlots: int32(available)}, nil
	}
	if req.ReservationID == "" || req.JobID == "" {
		return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "reservation identity required")
	}
	if w.cancelledAttempts[req.ReservationID] {
		return nil, rpc.NewRPCError(rpc.ErrCodeInvalidTransition, "attempt cancelled")
	}
	if _, ok := w.deploymentReceipts[req.ReservationID]; ok {
		return nil, rpc.NewRPCError(rpc.ErrCodeInvalidTransition, "reservation already consumed")
	}
	if req.MemoryMB > 0 {
		return nil, rpc.NewRPCError(rpc.ErrCodeInsufficientResources, "memory reservations are not supported")
	}
	if r := w.reservations[req.ReservationID]; r != nil {
		if r.jobID != req.JobID || r.epoch != req.EpochID || r.slots != int(req.RequiredSlots) {
			return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "reservation identity conflict")
		}
		return &rpc.RequestTaskSlotsResponse{ReservationID: req.ReservationID, Granted: int32(r.slots), AvailableSlots: int32(available), ExpiresAtMs: r.expires.UnixMilli()}, nil
	}
	if int(req.RequiredSlots) > available {
		return nil, rpc.NewRPCError(rpc.ErrCodeInsufficientSlots, "insufficient task slots")
	}
	ttl := 30 * time.Second
	if req.ReservationTimeoutMs > 0 {
		ttl = time.Duration(req.ReservationTimeoutMs) * time.Millisecond
	}
	r := &slotReservation{jobID: req.JobID, epoch: req.EpochID, slots: int(req.RequiredSlots), expires: now.Add(ttl)}
	w.reservations[req.ReservationID] = r
	return &rpc.RequestTaskSlotsResponse{ReservationID: req.ReservationID, Granted: req.RequiredSlots, AvailableSlots: int32(available - r.slots), ExpiresAtMs: r.expires.UnixMilli()}, nil
}

func (w *Worker) handleSubmitJob(ctx context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var req rpc.SubmitJobRequest
	if err := protocol.DecodeMsgPack(payload, &req); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
	}
	if req.JobID == "" || req.AttemptID == "" || req.ReservationID == "" || len(req.Tasks) == 0 {
		return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "reserved deployment identity and tasks required")
	}
	availableMemory, memoryErr := w.sampleStateMemory(ctx, req.Tasks)
	digest := sha256.Sum256(payload)
	w.mu.Lock()
	defer w.mu.Unlock()
	for {
		if ctx.Err() != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeTimeout, ctx.Err().Error())
		}
		if w.stopping {
			return nil, rpc.NewRPCError(rpc.ErrCodeWorkerShuttingDown, "worker stopping")
		}
		if req.EpochID != w.epoch {
			return nil, rpc.NewRPCError(rpc.ErrCodeStaleEpoch, "deployment epoch mismatch")
		}
		if w.cancelledAttempts[req.AttemptID] {
			return nil, rpc.NewRPCError(rpc.ErrCodeInvalidTransition, "deployment attempt cancelled")
		}
		if prior, ok := w.deploymentReceipts[req.AttemptID]; ok {
			if prior != digest {
				return nil, rpc.NewRPCError(rpc.ErrCodeDuplicateTask, "attempt reused with different deployment")
			}
			return &rpc.SubmitJobResponse{Accepted: true}, nil
		}
		w.availableSlotsLocked(time.Now())
		r := w.reservations[req.ReservationID]
		if r == nil || r.jobID != req.JobID || r.epoch != req.EpochID || r.slots != len(req.Tasks) {
			return nil, rpc.NewRPCError(rpc.ErrCodeInsufficientSlots, "reservation absent, expired or mismatched")
		}
		seen := make(map[string]bool)
		var previous *taskHandle
		for _, desc := range req.Tasks {
			if desc.TaskID == "" || seen[desc.TaskID] || desc.EpochID != req.EpochID || desc.AttemptID != req.AttemptID || len(desc.OperatorChain) == 0 {
				return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "invalid task identity or chain")
			}
			if h := w.tasks[desc.TaskID]; h != nil {
				if h.jobID != req.JobID || h.attemptID == req.AttemptID || h.done == nil {
					return nil, rpc.NewRPCError(rpc.ErrCodeDuplicateTask, "conflicting task identity")
				}
				previous = h
			}
			seen[desc.TaskID] = true
		}
		if previous != nil {
			// Terminal status can reach the coordinator before its reply lets the
			// old execution finish teardown. Keep the lease unconsumed while we
			// wait, without holding the lock needed by teardown and cancellation.
			timer := time.NewTimer(time.Until(r.expires))
			w.mu.Unlock()
			select {
			case <-previous.done:
			case <-ctx.Done():
			case <-timer.C:
			}
			timer.Stop()
			availableMemory, memoryErr = w.sampleStateMemory(ctx, req.Tasks)
			w.mu.Lock()
			// Recheck the entire admission, including epoch, cancellation, receipt
			// and lease expiry. Concurrent retries must not execute twice.
			continue
		}
		if memoryErr == nil {
			memoryErr = w.checkStateMemoryLocked(req.Tasks, availableMemory)
		}
		if memoryErr != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeInsufficientResources, memoryErr.Error())
		}
		if w.deploymentReceipts == nil {
			w.deploymentReceipts = make(map[string][32]byte)
		}
		w.deploymentReceipts[req.AttemptID] = digest
		delete(w.reservations, req.ReservationID)
		result := &rpc.SubmitJobResponse{Accepted: true}
		for _, desc := range req.Tasks {
			taskCtx, cancel := context.WithCancel(context.Background())
			w.installTaskLocked(req.JobID, desc.TaskID, desc, cancel)
			result.TaskStatuses = append(result.TaskStatuses, rpc.TaskDeploymentStatus{TaskID: desc.TaskID, Status: rpc.TaskStatusDeploying})
			go w.runTask(taskCtx, req.JobID, desc.TaskID, desc, w.log.With().Str("task_id", desc.TaskID).Logger())
		}
		return result, nil
	}
}

func (w *Worker) handleTriggerCheckpoint(ctx context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var req rpc.TriggerCheckpointRequest
	if err := protocol.DecodeMsgPack(payload, &req); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
	}
	if req.JobID == "" || req.CheckpointID == 0 {
		return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "checkpoint identity required")
	}
	w.mu.RLock()
	if req.EpochID != w.epoch {
		w.mu.RUnlock()
		return nil, rpc.NewRPCError(rpc.ErrCodeStaleEpoch, "checkpoint epoch mismatch")
	}
	var tasks []string
	for id, h := range w.tasks {
		if h.jobID == req.JobID && h.epoch == req.EpochID && h.checkpoint != nil {
			tasks = append(tasks, id)
		}
	}
	w.mu.RUnlock()
	if len(tasks) == 0 {
		return nil, rpc.NewRPCError(rpc.ErrCodeTaskNotRunning, "no checkpoint-capable tasks")
	}
	result := &rpc.TriggerCheckpointResponse{Accepted: true}
	for _, id := range tasks {
		if ctx.Err() != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeTimeout, ctx.Err().Error())
		}
		if !w.handleCheckpointCommand(rpc.WorkerCommand{Type: rpc.CommandTypeTakeSnapshot, TaskID: id, JobID: req.JobID, Data: payload}) {
			return nil, rpc.NewRPCError(rpc.ErrCodeTaskNotRunning, "checkpoint task ended before admission")
		}
		result.Statuses = append(result.Statuses, rpc.CheckpointTriggerStatus{TaskID: id, Success: true})
	}
	return result, nil
}
