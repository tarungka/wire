package engine

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// ImportArchive publishes a task checkpoint only after every referenced Pebble
// artifact has been imported and verified. The caller authenticates assignment
// ownership and verifies the transfer digest before invoking this method.
// Failed imports may leave verified, unreferenced content-addressed artifacts;
// they must not be treated as checkpoint records or deleted while still shared.
func (s *FileCheckpointStore) ImportArchive(ctx context.Context, jobID, taskID string, id, epoch uint64, source io.Reader, artifactRoot string, maxBytes int64) error {
	if _, err := s.path(jobID, taskID, id, epoch); err != nil {
		return err
	}
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return errors.New("invalid checkpoint archive quota")
	}
	limited := &io.LimitedReader{R: snapshotContextReader{ctx: ctx, reader: source}, N: maxBytes + 1}
	archive := tar.NewReader(limited)
	header, err := archive.Next()
	if err != nil {
		return err
	}
	if header.Name != "checkpoint.json" || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > maxStoredCheckpointBytes || header.Size > maxBytes {
		return ErrSnapshotCorrupt
	}
	var snapshot TaskCheckpoint
	decoder := json.NewDecoder(archive)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrSnapshotCorrupt
	}
	if snapshot.TaskID != taskID || snapshot.CheckpointID != id || snapshot.EpochID != epoch || (!snapshot.HasSource && len(snapshot.Source) != 0) {
		return ErrSnapshotCorrupt
	}
	if err := snapshot.ValidateStateHandles(); err != nil {
		return err
	}
	expected := make(map[string]int)
	for _, index := range snapshot.StateHandleIndexes {
		data := snapshot.Source
		if index >= 0 {
			data = snapshot.Operators[index]
		}
		var handle SnapshotHandle
		if err := json.Unmarshal(data, &handle); err != nil {
			return err
		}
		if handle.BackendType == StateBackendPebble {
			expected[fmt.Sprintf("artifacts/%d.tar", index)] = index
		}
	}
	replacements := make(map[int]SnapshotHandle)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		index, ok := expected[header.Name]
		if !ok || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > limited.N || header.Size > maxBytes {
			return ErrSnapshotCorrupt
		}
		handle, err := ImportPebbleSnapshot(ctx, archive, artifactRoot, header.Size)
		if err != nil {
			return err
		}
		replacements[index] = handle
		delete(expected, header.Name)
	}
	if len(expected) != 0 {
		return ErrSnapshotCorrupt
	}
	// Tar readers stop at the end marker. Check the remaining input too, so a
	// second archive or bytes beyond the agreed quota cannot be ignored.
	buffer := make([]byte, 32*1024)
	for {
		n, err := limited.Read(buffer)
		for _, value := range buffer[:n] {
			if value != 0 {
				return ErrSnapshotCorrupt
			}
		}
		if limited.N == 0 {
			return errors.New("checkpoint archive exceeds quota")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	snapshot, err = snapshot.RelocateStateHandles(replacements)
	if err != nil {
		return err
	}
	return s.Put(ctx, jobID, snapshot)
}
