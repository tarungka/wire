package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotImportRejectsUnsafeArchives(t *testing.T) {
	for _, mode := range []string{"path", "symlink", "checksum", "missing", "quota"} {
		t.Run(mode, func(t *testing.T) {
			name := "data"
			if mode == "path" {
				name = "../escape"
			}
			digest := sha256.Sum256([]byte("state"))
			manifest := pebbleSnapshotManifest{Version: 1, CheckpointID: 1, Files: map[string]string{name: hex.EncodeToString(digest[:])}}
			metadata, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var data bytes.Buffer
			writer := tar.NewWriter(&data)
			if err := writer.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(metadata))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(metadata); err != nil {
				t.Fatal(err)
			}
			if mode != "missing" {
				header := &tar.Header{Name: "files/" + name, Mode: 0600, Size: 5}
				if mode == "symlink" {
					header.Typeflag = tar.TypeSymlink
					header.Size = 0
					header.Linkname = "outside"
				}
				if err := writer.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if mode != "symlink" {
					value := "state"
					if mode == "checksum" {
						value = "wrong"
					}
					if _, err := writer.Write([]byte(value)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			quota := int64(data.Len())
			if mode == "quota" {
				quota--
			}
			root := t.TempDir()
			if _, err := ImportPebbleSnapshot(context.Background(), &data, root, quota); err == nil {
				t.Fatal("invalid archive accepted")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed import retained files: %v %v", entries, err)
			}
		})
	}
}

func TestSnapshotImportRetryUsesStableVerifiedHandle(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "data"), []byte("state"), 0600); err != nil {
		t.Fatal(err)
	}
	hashes, err := stateSnapshotHashes(source)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(pebbleSnapshotManifest{Version: 1, CheckpointID: 7, Path: source, Files: hashes})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportPebbleSnapshot(ctx, SnapshotHandle{CheckpointID: 7, BackendType: StateBackendPebble, Data: metadata}, &archive); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	handles := make(chan SnapshotHandle, 8)
	errors := make(chan error, 8)
	for range 8 {
		go func() {
			handle, err := ImportPebbleSnapshot(ctx, bytes.NewReader(archive.Bytes()), root, int64(archive.Len()))
			handles <- handle
			errors <- err
		}()
	}
	var first SnapshotHandle
	for i := range 8 {
		handle := <-handles
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = handle
		} else if !bytes.Equal(first.Data, handle.Data) {
			t.Fatal("retry changed relocated handle")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("replica directories: %v, %v", entries, err)
	}
	var relocated pebbleSnapshotManifest
	if err := json.Unmarshal(first.Data, &relocated); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relocated.Path, "data"), []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportPebbleSnapshot(ctx, bytes.NewReader(archive.Bytes()), root, int64(archive.Len())); err == nil {
		t.Fatal("reused corrupt existing artifact")
	}
}
