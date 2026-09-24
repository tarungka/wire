package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

func stateSnapshotHashes(path string) (map[string]string, error) {
	return stateSnapshotHashesContext(context.Background(), path)
}

func stateSnapshotHashesContext(ctx context.Context, path string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]string, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("snapshot contains non-regular file %q", entry.Name())
		}
		f, err := os.Open(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, snapshotContextReader{ctx: ctx, reader: f})
		closeErr := f.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		hashes[entry.Name()] = hex.EncodeToString(hash.Sum(nil))
	}
	return hashes, nil
}

func (b *PebbleStateBackend) Restore(handle SnapshotHandle) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return ErrBackendClosed
	}
	if handle.BackendType != StateBackendPebble {
		return fmt.Errorf("%w: expected pebble backend", ErrSnapshotCorrupt)
	}
	var manifest pebbleSnapshotManifest
	if err := json.Unmarshal(handle.Data, &manifest); err != nil {
		return fmt.Errorf("%w: %v", ErrSnapshotCorrupt, err)
	}
	if manifest.Version != 1 || manifest.CheckpointID != handle.CheckpointID || !filepath.IsAbs(manifest.Path) || len(manifest.Files) == 0 {
		return fmt.Errorf("%w: invalid manifest", ErrSnapshotCorrupt)
	}
	actual, err := stateSnapshotHashes(manifest.Path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSnapshotCorrupt, err)
	}
	if !reflect.DeepEqual(actual, manifest.Files) {
		return fmt.Errorf("%w: checkpoint file checksum mismatch", ErrSnapshotCorrupt)
	}
	path, err := os.MkdirTemp(b.root, "generation-")
	if err != nil {
		return err
	}
	// Keep an unsuccessful candidate only if ACTIVE may already name it.
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(path)
		}
	}()
	for name := range manifest.Files {
		if err = copyStateFile(filepath.Join(manifest.Path, name), filepath.Join(path, name)); err != nil {
			return err
		}
	}
	// Validate the copied bytes too, before opening (Pebble updates manifests).
	copied, err := stateSnapshotHashes(path)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(copied, manifest.Files) {
		return fmt.Errorf("%w: checkpoint changed while copying", ErrSnapshotCorrupt)
	}
	next, err := openRestoredState(path, b.maxCompactions)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSnapshotCorrupt, err)
	}
	published, err = publishStateGeneration(b.root, filepath.Base(path))
	if !published {
		_ = next.Close()
		return err
	}
	b.closeIterators()
	old := b.db
	oldDir := b.activeDir
	b.activeDir = path
	b.db = next
	closeErr := old.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.RemoveAll(oldDir)
}

func copyStateFile(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
