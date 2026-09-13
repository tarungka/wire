package engine

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// ImportPebbleSnapshot stages and verifies a portable snapshot under an existing
// private, durable root. maxArchiveBytes bounds the entire encoded archive.
// Only the returned handle identifies a complete snapshot; directory discovery
// must not treat incomplete staging directories as published checkpoints.
func ImportPebbleSnapshot(ctx context.Context, source io.Reader, root string, maxArchiveBytes int64) (SnapshotHandle, error) {
	if maxArchiveBytes <= 0 || maxArchiveBytes == math.MaxInt64 {
		return SnapshotHandle{}, errors.New("invalid snapshot archive quota")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return SnapshotHandle{}, err
	}
	limited := &io.LimitedReader{R: snapshotContextReader{ctx: ctx, reader: source}, N: maxArchiveBytes + 1}
	archive := tar.NewReader(limited)
	header, err := archive.Next()
	if err != nil {
		return SnapshotHandle{}, err
	}
	if header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > 1024*1024 {
		return SnapshotHandle{}, ErrSnapshotCorrupt
	}
	metadata, err := io.ReadAll(archive)
	if err != nil {
		return SnapshotHandle{}, err
	}
	var manifest pebbleSnapshotManifest
	if err := json.Unmarshal(metadata, &manifest); err != nil {
		return SnapshotHandle{}, err
	}
	if manifest.Version != 1 || manifest.CheckpointID == 0 || manifest.Path != "" || len(manifest.Files) == 0 || len(manifest.Files) > 10000 {
		return SnapshotHandle{}, ErrSnapshotCorrupt
	}
	for name, digest := range manifest.Files {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			return SnapshotHandle{}, ErrSnapshotCorrupt
		}
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return SnapshotHandle{}, ErrSnapshotCorrupt
		}
	}
	directory, err := os.MkdirTemp(root, "replica-snapshot-")
	if err != nil {
		return SnapshotHandle{}, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	seen := make(map[string]bool)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return SnapshotHandle{}, err
		}
		name := strings.TrimPrefix(header.Name, "files/")
		expected, exists := manifest.Files[name]
		if !exists || seen[name] || header.Name != "files/"+name || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > limited.N {
			return SnapshotHandle{}, ErrSnapshotCorrupt
		}
		if err := importSnapshotFile(archive, filepath.Join(directory, name), header.Size, expected); err != nil {
			return SnapshotHandle{}, err
		}
		seen[name] = true
	}
	// Consume trailing tar padding under the same quota. Nonzero trailing data
	// indicates an additional archive or payload and must not be ignored.
	buffer := make([]byte, 32*1024)
	for {
		n, err := limited.Read(buffer)
		for _, value := range buffer[:n] {
			if value != 0 {
				return SnapshotHandle{}, ErrSnapshotCorrupt
			}
		}
		if limited.N == 0 {
			return SnapshotHandle{}, errors.New("snapshot archive exceeds quota")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return SnapshotHandle{}, err
		}
	}
	if len(seen) != len(manifest.Files) {
		return SnapshotHandle{}, ErrSnapshotCorrupt
	}
	if err := ctx.Err(); err != nil {
		return SnapshotHandle{}, err
	}
	// Content-derived destinations keep relocated handles identical on retries.
	// Publish only after syncing all staged files and the directory itself.
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return SnapshotHandle{}, err
	}
	digest := sha256.Sum256(canonical)
	destination := filepath.Join(root, "snapshot-"+hex.EncodeToString(digest[:]))
	for _, path := range []string{directory, root} {
		if path == root {
			if err := os.Rename(directory, destination); err != nil {
				// A concurrent import or retry may already have published it.
				actual, verifyErr := stateSnapshotHashesContext(ctx, destination)
				if verifyErr != nil || !reflect.DeepEqual(actual, manifest.Files) {
					return SnapshotHandle{}, errors.Join(err, verifyErr, ErrSnapshotCorrupt)
				}
				if err := os.RemoveAll(directory); err != nil {
					return SnapshotHandle{}, err
				}
			}
		}
		dir, err := os.Open(path)
		if err != nil {
			return SnapshotHandle{}, err
		}
		syncErr := dir.Sync()
		closeErr := dir.Close()
		if err := errors.Join(syncErr, closeErr); err != nil {
			return SnapshotHandle{}, err
		}
	}
	manifest.Path = destination
	data, err := json.Marshal(manifest)
	if err != nil {
		return SnapshotHandle{}, err
	}
	complete = true
	return SnapshotHandle{CheckpointID: manifest.CheckpointID, BackendType: StateBackendPebble, Data: data}, nil
}

func importSnapshotFile(source io.Reader, path string, size int64, expected string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(file, hash), source, size); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return ErrSnapshotCorrupt
	}
	return errors.Join(file.Sync(), file.Close())
}
