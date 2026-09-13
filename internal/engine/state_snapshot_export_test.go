package engine

import (
	"bytes"
	"context"
	"os"
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
	handle, err = ImportPebbleSnapshot(context.Background(), &archive, t.TempDir(), 64*1024*1024)
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
