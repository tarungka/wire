package engine

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// ExportTaskCheckpoint packages metadata and portable Pebble artifacts. The
// caller owns destination cancellation and must discard partial output on error.
// Staging uses one temporary file at a time, bounded by maxBytes. maxBytes also
// limits the complete output, including headers and archive padding.
func ExportTaskCheckpoint(ctx context.Context, snapshot TaskCheckpoint, destination io.Writer, stagingRoot string, maxBytes int64) error {
	if maxBytes <= 0 {
		return errors.New("invalid checkpoint archive quota")
	}
	remaining := maxBytes - int64(len(snapshot.Source))
	for _, data := range snapshot.Operators {
		if int64(len(data)) > remaining {
			return errors.New("checkpoint metadata exceeds archive quota")
		}
		remaining -= int64(len(data))
	}
	if remaining < 0 || int64(len(snapshot.Operators)) > maxBytes/4 {
		return errors.New("checkpoint metadata exceeds archive quota")
	}
	if err := snapshot.ValidateStateHandles(); err != nil {
		return err
	}
	if snapshot.TaskID == "" || snapshot.CheckpointID == 0 || (!snapshot.HasSource && len(snapshot.Source) != 0) {
		return errors.New("invalid task checkpoint identity")
	}
	metadata, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	bounded := &checkpointQuotaWriter{destination: destination, remaining: maxBytes, ctx: ctx}
	archive := tar.NewWriter(bounded)
	if err := archive.WriteHeader(&tar.Header{Name: "checkpoint.json", Mode: 0600, Size: int64(len(metadata))}); err != nil {
		return err
	}
	if _, err := archive.Write(metadata); err != nil {
		return err
	}
	for _, index := range snapshot.StateHandleIndexes {
		data := snapshot.Source
		if index >= 0 {
			data = snapshot.Operators[index]
		}
		var handle SnapshotHandle
		if err := json.Unmarshal(data, &handle); err != nil {
			return err
		}
		if handle.BackendType != StateBackendPebble {
			continue
		}
		if err := exportCheckpointArtifact(ctx, archive, stagingRoot, index, handle, bounded.remaining); err != nil {
			return err
		}
	}
	return archive.Close()
}

func exportCheckpointArtifact(ctx context.Context, archive *tar.Writer, root string, index int, handle SnapshotHandle, quota int64) error {
	file, err := os.CreateTemp(root, ".checkpoint-artifact-")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err := ExportPebbleSnapshot(ctx, handle, &checkpointQuotaWriter{destination: file, remaining: quota, ctx: ctx}); err != nil {
		return err
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := archive.WriteHeader(&tar.Header{Name: fmt.Sprintf("artifacts/%d.tar", index), Mode: 0600, Size: size}); err != nil {
		return err
	}
	_, err = io.CopyN(archive, snapshotContextReader{ctx: ctx, reader: file}, size)
	return err
}

type checkpointQuotaWriter struct {
	destination io.Writer
	remaining   int64
	ctx         context.Context
}

func (w *checkpointQuotaWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(data)) > w.remaining {
		return 0, errors.New("checkpoint archive exceeds quota")
	}
	n, err := w.destination.Write(data)
	w.remaining -= int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, err
}
