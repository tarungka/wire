package worker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

type temporaryCheckpointArchive struct{ *os.File }

func (a temporaryCheckpointArchive) Close() error {
	err := a.File.Close()
	removeErr := os.Remove(a.Name())
	if err != nil {
		return err
	}
	return removeErr
}

func checkpointArchiveLoader(store *engine.FileCheckpointStore, stagingRoot string, authorize func(context.Context, rpc.FetchCheckpointRequest) error) rpc.CheckpointArchiveLoader {
	return func(ctx context.Context, fetch rpc.FetchCheckpointRequest) (rpc.ReplicateCheckpointRequest, io.ReadCloser, error) {
		var metadata rpc.ReplicateCheckpointRequest
		if err := authorize(ctx, fetch); err != nil {
			return metadata, nil, err
		}
		snapshot, err := store.Get(ctx, fetch.JobID, fetch.TaskID, fetch.CheckpointID, fetch.EpochID)
		if err != nil {
			return metadata, nil, err
		}
		file, err := os.CreateTemp(stagingRoot, ".checkpoint-recovery-")
		if err != nil {
			return metadata, nil, err
		}
		archive := temporaryCheckpointArchive{file}
		success := false
		defer func() {
			if !success {
				_ = archive.Close()
			}
		}()
		hash := sha256.New()
		if err := engine.ExportTaskCheckpoint(ctx, snapshot, io.MultiWriter(file, hash), stagingRoot, rpc.MaxCheckpointTransferSize); err != nil {
			return metadata, nil, err
		}
		size, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return metadata, nil, err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return metadata, nil, err
		}
		metadata = rpc.ReplicateCheckpointRequest{Format: rpc.CheckpointFormatArchive, JobID: fetch.JobID, TaskID: fetch.TaskID, CheckpointID: fetch.CheckpointID, EpochID: fetch.EpochID, Size: uint64(size)}
		copy(metadata.SHA256[:], hash.Sum(nil))
		success = true
		return metadata, archive, nil
	}
}

// fetchTaskCheckpoint imports into worker-owned storage before operator Open or
// task RUNNING. A failed transfer never falls back to starting with empty state.
func (w *Worker) fetchTaskCheckpoint(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor) (*engine.TaskCheckpoint, error) {
	restore := desc.RestoreCheckpoint
	if restore == nil || restore.ReplicaAddress == "" {
		return nil, fmt.Errorf("checkpoint recovery requires a replica address")
	}
	cfg := w.cfg.CheckpointReplica
	if cfg == nil {
		return nil, fmt.Errorf("checkpoint recovery requires local storage")
	}
	sourceTaskID, targetTaskID := taskID, ""
	if restore.SourceTaskID != "" {
		sourceTaskID, targetTaskID = restore.SourceTaskID, taskID
	}
	request := rpc.FetchCheckpointRequest{AttemptID: desc.AttemptID, WorkerID: w.cfg.WorkerID, DeploymentEpoch: desc.EpochID, JobID: jobID, TaskID: sourceTaskID, TargetTaskID: targetTaskID, CheckpointID: restore.CheckpointID, EpochID: restore.EpochID}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	session, err := transport.NewClientSessionContext(ctx, restore.ReplicaAddress, transport.DefaultConfig())
	if err != nil {
		return nil, err
	}
	defer session.Close()
	file, err := os.CreateTemp(cfg.StagingRoot, ".checkpoint-fetch-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err := rpc.NewClient(session.YamuxSession(), rpc.DefaultConfig()).FetchCheckpoint(ctx, request, file); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	store, err := engine.NewFileCheckpointStore(cfg.StoreRoot)
	if err != nil {
		return nil, err
	}
	if err := store.ImportArchive(ctx, jobID, sourceTaskID, restore.CheckpointID, restore.EpochID, file, cfg.ArtifactRoot, rpc.MaxCheckpointTransferSize); err != nil {
		return nil, err
	}
	snapshot, err := store.Get(ctx, jobID, sourceTaskID, restore.CheckpointID, restore.EpochID)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}
