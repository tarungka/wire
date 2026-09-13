package engine

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ExportPebbleSnapshot writes a portable archive of an immutable checkpoint.
// The archive manifest contains no originating-worker path. A receiver must
// stage and verify the complete archive before publishing a relocated handle.
func ExportPebbleSnapshot(ctx context.Context, handle SnapshotHandle, destination io.Writer) error {
	if handle.BackendType != StateBackendPebble {
		return errors.New("portable export requires a Pebble snapshot")
	}
	var manifest pebbleSnapshotManifest
	if err := json.Unmarshal(handle.Data, &manifest); err != nil {
		return err
	}
	if manifest.Version != 1 || manifest.CheckpointID != handle.CheckpointID || !filepath.IsAbs(manifest.Path) || len(manifest.Files) == 0 {
		return ErrSnapshotCorrupt
	}
	names := make([]string, 0, len(manifest.Files))
	for name, hash := range manifest.Files {
		if name == "." || name == ".." || filepath.Base(name) != name || name == "" {
			return ErrSnapshotCorrupt
		}
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return ErrSnapshotCorrupt
		}
		names = append(names, name)
	}
	sort.Strings(names)
	source := manifest.Path
	manifest.Path = ""
	metadata, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	archive := tar.NewWriter(destination)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(metadata))}); err != nil {
		return err
	}
	if _, err := archive.Write(metadata); err != nil {
		return err
	}
	for _, name := range names {
		if err := exportSnapshotFile(ctx, archive, filepath.Join(source, name), name, manifest.Files[name]); err != nil {
			return err
		}
	}
	return archive.Close()
}

func exportSnapshotFile(ctx context.Context, archive *tar.Writer, path, name, expected string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: non-regular snapshot file", ErrSnapshotCorrupt)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := archive.WriteHeader(&tar.Header{Name: "files/" + name, Mode: 0600, Size: info.Size()}); err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(archive, hash), snapshotContextReader{ctx: ctx, reader: file}, info.Size()); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("%w: snapshot file changed during export", ErrSnapshotCorrupt)
	}
	return ctx.Err()
}

type snapshotContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r snapshotContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
