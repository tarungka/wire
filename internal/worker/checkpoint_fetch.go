package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

var errCheckpointUnavailable = errors.New("checkpoint state unavailable")

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
			return metadata, nil, rpc.NewRPCError(rpc.ErrCodeUnknownCheckpoint, err.Error())
		}
		original, archiveErr := store.OpenArchive(ctx, fetch.JobID, fetch.TaskID, fetch.CheckpointID, fetch.EpochID)
		if archiveErr == nil {
			hash := sha256.New()
			size, err := io.Copy(hash, original)
			if err == nil {
				_, err = original.Seek(0, io.SeekStart)
			}
			if err != nil {
				_ = original.Close()
				return metadata, nil, err
			}
			metadata = rpc.ReplicateCheckpointRequest{Format: rpc.CheckpointFormatArchive, JobID: fetch.JobID, TaskID: fetch.TaskID, CheckpointID: fetch.CheckpointID, EpochID: fetch.EpochID, Size: uint64(size)}
			copy(metadata.SHA256[:], hash.Sum(nil))
			return metadata, original, nil
		}
		if fetch.RequireArchive {
			return metadata, nil, rpc.NewRPCError(rpc.ErrCodeUnknownCheckpoint, archiveErr.Error())
		}
		if !errors.Is(archiveErr, os.ErrNotExist) {
			return metadata, nil, archiveErr
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
	request := rpc.FetchCheckpointRequest{RequireArchive: restore.ArchiveSHA256 != "", AttemptID: desc.AttemptID, WorkerID: w.cfg.WorkerID, DeploymentEpoch: desc.EpochID, JobID: jobID, TaskID: sourceTaskID, TargetTaskID: targetTaskID, CheckpointID: restore.CheckpointID, EpochID: restore.EpochID}
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
		var remote *rpc.RPCError
		if errors.As(err, &remote) && remote.Code == rpc.ErrCodeUnknownCheckpoint {
			return nil, fmt.Errorf("%w: %v", errCheckpointUnavailable, err)
		}
		return nil, err
	}
	if restore.ArchiveSHA256 != "" {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		digest := sha256.New()
		size, err := io.Copy(digest, file)
		if err != nil {
			return nil, err
		}
		if size != restore.ArchiveSize || hex.EncodeToString(digest.Sum(nil)) != restore.ArchiveSHA256 {
			return nil, fmt.Errorf("%w: archive does not match completed manifest", errCheckpointUnavailable)
		}
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
