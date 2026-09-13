package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskCheckpointArchiveIncludesPortableArtifact(t *testing.T) {
	ctx := context.Background()
	origin := t.TempDir()
	if err := os.WriteFile(filepath.Join(origin, "data"), []byte("state"), 0600); err != nil {
		t.Fatal(err)
	}
	hashes, err := stateSnapshotHashes(origin)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(pebbleSnapshotManifest{Version: 1, CheckpointID: 7, Path: origin, Files: hashes})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := json.Marshal(SnapshotHandle{CheckpointID: 7, BackendType: StateBackendPebble, Data: manifest})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, Operators: [][]byte{[]byte("opaque"), handle}, StateHandleIndexes: []int{1}}
	staging := t.TempDir()
	var encoded bytes.Buffer
	if err := ExportTaskCheckpoint(ctx, snapshot, &encoded, staging, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	for range 2 {
		if err := store.ImportArchive(ctx, "job", "task", 7, 2, bytes.NewReader(encoded.Bytes()), artifactRoot, int64(encoded.Len())); err != nil {
			t.Fatalf("durable archive import/retry: %v", err)
		}
	}
	if err := store.ImportArchive(ctx, "job", "task", 7, 3, bytes.NewReader(encoded.Bytes()), artifactRoot, int64(encoded.Len())); err == nil {
		t.Fatal("wrong execution epoch accepted")
	}
	var incomplete bytes.Buffer
	missing := tar.NewWriter(&incomplete)
	metadata, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.WriteHeader(&tar.Header{Name: "checkpoint.json", Mode: 0600, Size: int64(len(metadata))}); err != nil {
		t.Fatal(err)
	}
	if _, err := missing.Write(metadata); err != nil {
		t.Fatal(err)
	}
	if err := missing.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportArchive(ctx, "missing", "task", 7, 2, &incomplete, artifactRoot, 1<<20); err == nil {
		t.Fatal("checkpoint with missing artifact published")
	}
	if _, err := store.Get(ctx, "missing", "task", 7, 2); !os.IsNotExist(err) {
		t.Fatalf("incomplete checkpoint is visible: %v", err)
	}
	archive := tar.NewReader(bytes.NewReader(encoded.Bytes()))
	header, err := archive.Next()
	if err != nil || header.Name != "checkpoint.json" {
		t.Fatalf("metadata header: %v, %v", header, err)
	}
	var received TaskCheckpoint
	if err := json.NewDecoder(archive).Decode(&received); err != nil {
		t.Fatal(err)
	}
	header, err = archive.Next()
	if err != nil || header.Name != "artifacts/1.tar" {
		t.Fatalf("artifact header: %v, %v", header, err)
	}
	relocated, err := ImportPebbleSnapshot(ctx, archive, t.TempDir(), header.Size)
	if err != nil {
		t.Fatal(err)
	}
	received, err = received.RelocateStateHandles(map[int]SnapshotHandle{1: relocated})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received.Operators[0], snapshot.Operators[0]) {
		t.Fatal("opaque state changed")
	}
	if _, err := archive.Next(); err != io.EOF {
		t.Fatalf("trailing entry: %v", err)
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging leaked: %v, %v", entries, err)
	}
	if err := ExportTaskCheckpoint(ctx, snapshot, io.Discard, staging, 1); err == nil {
		t.Fatal("quota ignored")
	}
}

func TestInlineImportRejectsFileBackedHandles(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handle, err := json.Marshal(SnapshotHandle{CheckpointID: 7, BackendType: StateBackendPebble, Data: []byte("unverified local path")})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []bool{false, true} {
		snapshot := TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2}
		if source {
			snapshot.HasSource = true
			snapshot.Source = handle
			snapshot.StateHandleIndexes = []int{-1}
		} else {
			snapshot.Operators = [][]byte{handle}
			snapshot.StateHandleIndexes = []int{0}
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Import(ctx, "job", "task", 7, 2, bytes.NewReader(encoded)); err == nil {
			t.Fatal("unverified file-backed snapshot accepted")
		}
		if _, err := store.Get(ctx, "job", "task", 7, 2); !os.IsNotExist(err) {
			t.Fatalf("unverified checkpoint published: %v", err)
		}
	}
}
