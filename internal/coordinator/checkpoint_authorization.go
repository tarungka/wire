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
	if !c.readyLocked() || request.Snapshot.EpochID != c.epoch {
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

// HandleAuthorizeCheckpointFetch permits recovery only from the recorded
// completed replica to the worker currently assigned the task in this epoch.
func (c *Coordinator) HandleAuthorizeCheckpointFetch(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
	var request rpc.AuthorizeCheckpointFetchRequest
	if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &request); err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
	}
	denied := &rpc.AcknowledgeCheckpointResponse{Message: "checkpoint recovery is not assigned"}
	fetch := request.Fetch
	if err := fetch.Validate(); err != nil {
		return denied, nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.readyLocked() || fetch.DeploymentEpoch != c.epoch {
		return denied, nil
	}
	replica := c.workers[request.ReplicaWorkerID]
	targetJobID := fetch.JobID
	crossJob := fetch.TargetJobID != "" && fetch.TargetJobID != fetch.JobID
	if crossJob {
		targetJobID = fetch.TargetJobID
	}
	job := c.jobs[targetJobID]
	if replica == nil || replica.CheckpointAddress == "" || job == nil || job.LatestCheckpoint != fetch.CheckpointID || (job.Status != JobDeploying && job.Status != JobRunning) {
		return denied, nil
	}
	if crossJob {
		source := c.jobs[fetch.JobID]
		ref := job.RestoreSavepoint
		if source == nil || !source.Status.IsTerminal() || source.UpgradeSuccessorID != targetJobID || source.LatestCheckpoint != fetch.CheckpointID || ref == nil || ref.JobID != fetch.JobID || ref.CheckpointID != fetch.CheckpointID {
			return denied, nil
		}
		sp, err := c.GetSavepoint(ref.JobID, ref.SavepointID)
		if err != nil || sp.JobID != ref.JobID || sp.ID != ref.SavepointID || sp.Status != SavepointCompleted || sp.CheckpointID != fetch.CheckpointID || sp.EpochID != fetch.EpochID {
			return denied, nil
		}
	}
	data, err := c.store.Get(CheckpointKey(fetch.JobID, fetch.CheckpointID))
	if err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, err.Error())
	}
	var checkpoint CheckpointMeta
	if err := protocol.DecodeMsgPack(data, &checkpoint); err != nil {
		return denied, nil
	}
	if checkpoint.JobID != fetch.JobID || checkpoint.ID != fetch.CheckpointID || checkpoint.EpochID != fetch.EpochID || checkpoint.Status != CheckpointCompleted || checkpoint.InvalidReason != "" || checkpoint.Tasks[fetch.TaskID] == "" || checkpoint.StatePaths[fetch.TaskID] != replica.CheckpointAddress || checkpoint.Replicas[fetch.TaskID] != replica.CheckpointAddress {
		return denied, nil
	}
	if crossJob && checkpoint.SavepointID != job.RestoreSavepoint.SavepointID {
		return denied, nil
	}
	data, err = c.store.Get(JobAssignmentsKey(targetJobID))
	if err != nil {
		return nil, rpc.NewRPCError(rpc.ErrCodeInternalError, err.Error())
	}
	var assignment TaskAssignmentMap
	if err := protocol.DecodeMsgPack(data, &assignment); err != nil {
		return denied, nil
	}
	target := fetch.TaskID
	if fetch.TargetTaskID != "" {
		target = fetch.TargetTaskID
	}
	if assignment.JobID != targetJobID || assignment.Assignments[target] != fetch.WorkerID || assignment.AttemptID != fetch.AttemptID {
		return denied, nil
	}
	if crossJob {
		restore, ok := assignment.RestoreCheckpoints[target]
		if !ok || !fetch.RequireArchive || restore.ArchiveSHA256 == "" || restore.ArchiveSize <= 0 || restore.SourceJobID != fetch.JobID || restore.SourceTaskID != fetch.TaskID || restore.CheckpointID != fetch.CheckpointID || restore.EpochID != fetch.EpochID || restore.ReplicaAddress != replica.CheckpointAddress || assignment.EpochID != fetch.DeploymentEpoch {
			return denied, nil
		}
		return &rpc.AcknowledgeCheckpointResponse{Accepted: true}, nil
	}
	if fetch.TargetTaskID != "" {
		permitted := false
		for _, part := range assignment.RescaleParts[target] {
			if part.SourceTaskID == fetch.TaskID && part.ReplicaAddress == replica.CheckpointAddress {
				permitted = true
				break
			}
		}
		if !permitted {
			return denied, nil
		}
	}
	return &rpc.AcknowledgeCheckpointResponse{Accepted: true}, nil
}
