package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
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
