package engine

import (
	"context"
	"errors"
	"os"
)

var ErrCheckpointDeleted = errors.New("checkpoint was deleted")

func checkpointNotDeleted(path string) error {
	_, err := os.Lstat(path + ".deleted")
	if err == nil {
		return ErrCheckpointDeleted
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Delete durably fences this exact replica identity before removing its inline
// snapshot and retained archive. Retries complete interrupted removal. Shared
// content-addressed Pebble artifacts are deliberately retained until a separate
// reference-aware collector can prove they are unused. The caller must authorize
// deletion and establish that no live recovery reference needs this identity.
func (s *FileCheckpointStore) Delete(ctx context.Context, jobID, taskID string, id, epoch uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(jobID, taskID, id, epoch)
	if err != nil {
		return err
	}
	marker, err := os.OpenFile(path+".deleted", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		syncErr := marker.Sync()
		closeErr := marker.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	// Persist the fence before making the payload unavailable, including retry.
	if err := dir.Sync(); err != nil {
		return err
	}
	for _, name := range []string{path, path + ".archive"} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return dir.Sync()
}
