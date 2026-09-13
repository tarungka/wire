package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

type checkpointReplicaClient interface {
	ReplicateCheckpoint(context.Context, rpc.ReplicateCheckpointRequest, io.Reader) error
}

// inlineCheckpointReplicator transfers self-contained snapshot bytes. It must
// not be used for operators whose snapshots reference local files; those need
// artifact replication before the snapshot receipt can establish durability.
type inlineCheckpointReplicator struct {
	jobID, taskID string
	epoch         uint64
	client        checkpointReplicaClient
}

var _ engine.CheckpointReplicator = (*inlineCheckpointReplicator)(nil)

func (r *inlineCheckpointReplicator) Replicate(ctx context.Context, snapshot engine.TaskCheckpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.TaskID != r.taskID || snapshot.EpochID != r.epoch {
		return errors.New("checkpoint does not belong to task execution")
	}
	if r.client == nil {
		return errors.New("checkpoint replica client is unavailable")
	}
	if !snapshot.HasSource && len(snapshot.Source) != 0 {
		return errors.New("source snapshot marker missing")
	}
	size := uint64(len(snapshot.Source)) + uint64(len(snapshot.Operators))*4
	for _, operator := range snapshot.Operators {
		size += uint64(len(operator))
	}
	if size > rpc.MaxCheckpointTransferSize {
		return errors.New("checkpoint exceeds transfer size limit")
	}
	// Validate identity before allocating the serialized snapshot.
	request := rpc.ReplicateCheckpointRequest{JobID: r.jobID, TaskID: r.taskID, CheckpointID: snapshot.CheckpointID, EpochID: r.epoch, Size: 1}
	if err := request.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	request.Size = uint64(len(payload))
	if err := request.Validate(); err != nil {
		return err
	}
	request.SHA256 = sha256.Sum256(payload)
	return r.client.ReplicateCheckpoint(ctx, request, bytes.NewReader(payload))
}
