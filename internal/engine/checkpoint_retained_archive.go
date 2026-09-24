package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// ImportArchive retains the exact verified transfer bytes, so relocation of
// typed state handles never changes the portable archive referenced by a manifest.
func (s *FileCheckpointStore) ImportArchive(ctx context.Context, jobID, taskID string, id, epoch uint64, source io.Reader, artifactRoot string, maxBytes int64) error {
	s.artifactsMu.RLock()
	defer s.artifactsMu.RUnlock()
	destination, err := s.path(jobID, taskID, id, epoch)
	if err != nil {
		return err
	}
	if err := checkpointNotDeleted(destination); err != nil {
		return err
	}
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return errors.New("invalid checkpoint archive quota")
	}
	file, err := os.CreateTemp(s.root, ".archive-pending-")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	n, err := io.Copy(file, io.LimitReader(snapshotContextReader{ctx: ctx, reader: source}, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return errors.New("checkpoint archive exceeds quota")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err = s.importArchive(ctx, jobID, taskID, id, epoch, file, artifactRoot, maxBytes); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	destination += ".archive"
	if err = os.Link(file.Name(), destination); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, err := s.OpenArchive(ctx, jobID, taskID, id, epoch)
		if err != nil {
			return err
		}
		defer existing.Close()
		a, b := sha256.New(), sha256.New()
		if _, err = io.Copy(a, snapshotContextReader{ctx: ctx, reader: existing}); err != nil {
			return err
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err = io.Copy(b, snapshotContextReader{ctx: ctx, reader: file}); err != nil {
			return err
		}
		if string(a.Sum(nil)) != string(b.Sum(nil)) {
			return ErrCheckpointConflict
		}
	}
	if err := checkpointNotDeleted(destination[:len(destination)-len(".archive")]); err != nil {
		if errors.Is(err, ErrCheckpointDeleted) {
			_ = os.Remove(destination)
		}
		return err
	}
	if err = os.Remove(file.Name()); err != nil {
		return err
	}
	root, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Sync()
}

// OpenArchive returns the original portable bytes using a contained filesystem
// open. The caller verifies its digest against the completed manifest before use.
func (s *FileCheckpointStore) OpenArchive(ctx context.Context, jobID, taskID string, id, epoch uint64) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := s.path(jobID, taskID, id, epoch)
	if err != nil {
		return nil, err
	}
	if err := checkpointNotDeleted(name); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name = filepath.Base(name) + ".archive"
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, fmt.Errorf("%w: non-regular archive", ErrCheckpointFileCorrupt)
	}
	return root.Open(name)
}
