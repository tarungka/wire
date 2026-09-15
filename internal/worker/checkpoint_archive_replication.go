package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// archiveCheckpointReplicator stages a bounded package before transfer so its
// length and digest are known before receiver admission. Only the RPC receipt
// establishes remote durability. Temporary packages are removed on every exit.
type archiveCheckpointReplicator struct {
	mu            sync.Mutex
	inventory     []byte
	inventoryID   uint64
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
	if err := r.client.ReplicateCheckpoint(ctx, request, file); err != nil {
		return err
	}
	identity := sha256.Sum256([]byte(snapshot.TaskID))
	taskManifest := engine.TaskMeta{SinkPrepared: snapshot.SinkPrepared, SinkCommittedCheckpoint: snapshot.SinkCommittedCheckpoint, TaskID: snapshot.TaskID, StatePath: hex.EncodeToString(identity[:]), StateSizeBytes: size, StateFiles: []string{"checkpoint.archive"}, StateSHA256: map[string]string{"checkpoint.archive": hex.EncodeToString(request.SHA256[:])}}
	if snapshot.HasSource {
		taskManifest.SourceOffsets = json.RawMessage(`{"state_file":"checkpoint.archive","member":"checkpoint.json","field":"Source"}`)
	}
	inventory, err := json.Marshal(taskManifest)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.inventoryID, r.inventory = snapshot.CheckpointID, inventory
	r.mu.Unlock()
	return nil
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

func (r *archiveCheckpointReplicator) manifest(id uint64) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inventoryID != id {
		return nil
	}
	return append([]byte(nil), r.inventory...)
}
