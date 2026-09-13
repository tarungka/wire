package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortablePebbleSnapshotSurvivesOriginalRemoval(t *testing.T) {
	root := t.TempDir()
	backend := pebbleBackendForTest(t, root)
	if err := backend.Put([]byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	handle, err := backend.Checkpoint(7)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportPebbleSnapshot(context.Background(), handle, &archive); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	// Extract only the exporter-produced files to model a distinct peer. The
	// production receiver still needs its own untrusted-archive validation.
	received := t.TempDir()
	reader := tar.NewReader(&archive)
	var manifest pebbleSnapshotManifest
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "manifest.json" {
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.Path != "" {
				t.Fatal("archive leaked original path")
			}
		} else {
			name := strings.TrimPrefix(header.Name, "files/")
			if filepath.Base(name) != name {
				t.Fatal("unsafe archive path")
			}
			if err := os.WriteFile(filepath.Join(received, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest.Path = received
	handle.Data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	restored := pebbleBackendForTest(t, t.TempDir())
	defer func() { _ = restored.Close() }()
	if err := restored.Restore(handle); err != nil {
		t.Fatal(err)
	}
	value, err := restored.Get([]byte("key"))
	if err != nil || string(value) != "value" {
		t.Fatalf("portable recovery: %q %v", value, err)
	}
}
