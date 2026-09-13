package worker

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// archiveCheckpointReplicator stages a bounded package before transfer so its
// length and digest are known before receiver admission. Only the RPC receipt
// establishes remote durability. Temporary packages are removed on every exit.
type archiveCheckpointReplicator struct {
	jobID, taskID string
	epoch         uint64
	stagingRoot   string
	client        checkpointReplicaClient
}

var _ engine.CheckpointReplicator = (*archiveCheckpointReplicator)(nil)

func (r *archiveCheckpointReplicator) Replicate(ctx context.Context, snapshot engine.TaskCheckpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.TaskID != r.taskID || snapshot.EpochID != r.epoch {
		return errors.New("checkpoint does not belong to task execution")
	}
	if r.client == nil {
		return errors.New("checkpoint replica client is unavailable")
	}
	request := rpc.ReplicateCheckpointRequest{Format: rpc.CheckpointFormatArchive, JobID: r.jobID, TaskID: r.taskID, CheckpointID: snapshot.CheckpointID, EpochID: r.epoch, Size: 1}
	if err := request.Validate(); err != nil {
		return err
	}
	file, err := os.CreateTemp(r.stagingRoot, ".checkpoint-package-")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	hash := sha256.New()
	if err := engine.ExportTaskCheckpoint(ctx, snapshot, io.MultiWriter(file, hash), r.stagingRoot, rpc.MaxCheckpointTransferSize); err != nil {
		return err
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	request.Size = uint64(size)
	copy(request.SHA256[:], hash.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return r.client.ReplicateCheckpoint(ctx, request, file)
}

// publishCheckpointReplica is called after RPC chunk verification. The owning
// worker must fence/authenticate the assignment before allowing publication.
func publishCheckpointReplica(ctx context.Context, store *engine.FileCheckpointStore, artifactRoot string, request rpc.ReplicateCheckpointRequest, body io.Reader) error {
	if err := request.Validate(); err != nil {
		return err
	}
	switch request.Format {
	case rpc.CheckpointFormatInline:
		return store.Import(ctx, request.JobID, request.TaskID, request.CheckpointID, request.EpochID, body)
	case rpc.CheckpointFormatArchive:
		return store.ImportArchive(ctx, request.JobID, request.TaskID, request.CheckpointID, request.EpochID, body, artifactRoot, int64(request.Size))
	default:
		return errors.New("unsupported checkpoint replica format")
	}
}
