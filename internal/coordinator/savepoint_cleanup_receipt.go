package coordinator

import (
	"context"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// HandleAcknowledgeCheckpointCleanup retires only an exact durably requested
// replica deletion. A failed receipt write leaves the request pending for retry.
func (c *Coordinator) HandleAcknowledgeCheckpointCleanup(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var request rpc.CheckpointCleanupRequest
	if err := protocol.DecodeMsgPack(payload, &request); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
	}
	denied := &rpc.AcknowledgeCheckpointResponse{Accepted: false, Message: "cleanup receipt does not match pending replica"}
	c.mu.Lock()
	defer c.mu.Unlock()
	worker := c.workers[request.WorkerID]
	if !c.readyLocked() || request.EpochID != c.epoch || worker == nil || worker.Lost || worker.Removed || request.TaskID == "" || request.CheckpointID == 0 {
		return denied, nil
	}
	raw, err := c.store.Get(savepointCleanupKey(request.JobID, request.SavepointID))
	if err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, err.Error())
	}
	if len(raw) == 0 {
		return denied, nil
	}
	var cleanup SavepointCleanup
	if err := protocol.DecodeMsgPack(raw, &cleanup); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, err.Error())
	}
	if cleanup.JobID != request.JobID || cleanup.SavepointID != request.SavepointID || cleanup.CheckpointID != request.CheckpointID || cleanup.EpochID != request.SnapshotEpoch || cleanup.Replicas[request.TaskID] == "" || cleanup.Replicas[request.TaskID] != worker.CheckpointAddress {
		return denied, nil
	}
	if cleanup.Completed[request.TaskID] {
		return &rpc.AcknowledgeCheckpointResponse{Accepted: true}, nil
	}
	if cleanup.Completed == nil {
		cleanup.Completed = make(map[string]bool)
	}
	cleanup.Completed[request.TaskID] = true
	all := true
	for task := range cleanup.Replicas {
		if !cleanup.Completed[task] {
			all = false
			break
		}
	}
	if all {
		cleanup.CompletedAt = time.Now().UTC()
	}
	raw, err = protocol.EncodeMsgPack(cleanup)
	if err == nil {
		err = c.store.Set(savepointCleanupKey(cleanup.JobID, cleanup.SavepointID), raw)
	}
	if err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, err.Error())
	}
	return &rpc.AcknowledgeCheckpointResponse{Accepted: true}, nil
}
