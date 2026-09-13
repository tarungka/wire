package coordinator

import (
	"context"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func (c *Coordinator) HandleAuthorizeCheckpointReplica(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var request rpc.AuthorizeCheckpointReplicaRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &request); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
	}
	denied := &rpc.AcknowledgeCheckpointResponse{Accepted: false, Message: "checkpoint replica is not assigned"}
	if err := request.Snapshot.Validate(); err != nil {
		return denied, nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.state != StateLeader || !c.recovered || request.Snapshot.EpochID != c.epoch {
		return denied, nil
	}
	worker := c.workers[request.WorkerID]
	if worker == nil || worker.CheckpointAddress == "" {
		return denied, nil
	}
	data, err := c.store.Get(CheckpointKey(request.Snapshot.JobID, request.Snapshot.CheckpointID))
	if err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, err.Error())
	}
	var checkpoint CheckpointMeta
	if err := protocol.DecodeMsgPack(data, &checkpoint); err != nil {
		return denied, nil
	}
	if checkpoint.JobID != request.Snapshot.JobID || checkpoint.ID != request.Snapshot.CheckpointID || checkpoint.EpochID != request.Snapshot.EpochID || checkpoint.Status != CheckpointInProgress {
		return denied, nil
	}
	if checkpoint.Replicas[request.Snapshot.TaskID] != worker.CheckpointAddress || checkpoint.Tasks[request.Snapshot.TaskID] == request.WorkerID || checkpoint.Tasks[request.Snapshot.TaskID] == "" {
		return denied, nil
	}
	return &rpc.AcknowledgeCheckpointResponse{Accepted: true}, nil
}
