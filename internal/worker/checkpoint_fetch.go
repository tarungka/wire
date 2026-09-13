package worker

import (
	"context"
	"crypto/sha256"
	"io"
	"os"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
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
