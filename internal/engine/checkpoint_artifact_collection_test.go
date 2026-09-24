package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactCollectionRetainsSharedCheckpointImports(t *testing.T) {
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin := t.TempDir()
	artifacts := t.TempDir()
	if err := os.WriteFile(filepath.Join(origin, "data"), []byte("shared state"), 0600); err != nil {
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
	for _, task := range []string{"a", "b"} {
		snapshot := TaskCheckpoint{TaskID: task, CheckpointID: 7, EpochID: 2, Operators: [][]byte{handle}, StateHandleIndexes: []int{0}}
		var archive bytes.Buffer
		if err := ExportTaskCheckpoint(t.Context(), snapshot, &archive, t.TempDir(), 1<<20); err != nil {
			t.Fatal(err)
		}
		if err := store.ImportArchive(t.Context(), "job", task, 7, 2, &archive, artifacts, 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(artifacts)
	if err != nil || len(entries) != 1 {
		t.Fatalf("shared imports=%v err=%v", entries, err)
	}
	shared := filepath.Join(artifacts, entries[0].Name())
	if err := store.Delete(t.Context(), "job", "a", 7, 2); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileCheckpointStore(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := reopened.CollectArtifacts(t.Context(), artifacts); err != nil || removed != 0 {
		t.Fatalf("live reference collected: %d %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(shared, "data")); err != nil {
		t.Fatal(err)
	}
	// Corruption anywhere in retained metadata must prevent collection.
	if err := os.WriteFile(filepath.Join(store.root, "bad.checkpoint"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CollectArtifacts(t.Context(), artifacts); err == nil {
		t.Fatal("corrupt inventory accepted")
	}
	if err := os.Remove(filepath.Join(store.root, "bad.checkpoint")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), "job", "b", 7, 2); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.CollectArtifacts(t.Context(), artifacts); err != nil || removed != 1 {
		t.Fatalf("unused artifact retained: %d %v", removed, err)
	}
	if _, err := os.Stat(shared); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact remains: %v", err)
	}
	if removed, err := store.CollectArtifacts(t.Context(), artifacts); err != nil || removed != 0 {
		t.Fatalf("retry: %d %v", removed, err)
	}
}

func TestArtifactCollectionDoesNotFollowSymlinksOrRemoveStaging(t *testing.T) {
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep")
	if err := os.WriteFile(sentinel, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"replica-snapshot-in-progress", "application-data"} {
		if err := os.Mkdir(filepath.Join(artifacts, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(artifacts, "snapshot-"+strings.Repeat("a", 64))
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CollectArtifacts(t.Context(), artifacts); err == nil {
		t.Fatal("symlink artifact accepted")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "outside" {
		t.Fatalf("outside data changed: %q %v", data, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.CollectArtifacts(t.Context(), artifacts); err != nil || removed != 0 {
		t.Fatalf("unrelated collection: %d %v", removed, err)
	}
	for _, name := range []string{"replica-snapshot-in-progress", "application-data"} {
		if _, err := os.Stat(filepath.Join(artifacts, name)); err != nil {
			t.Fatal("removed unrelated directory", err)
		}
	}
}

func TestReopenedCheckpointRootAliasesShareCollectionGate(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	first, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileCheckpointStore(alias)
	if err != nil {
		t.Fatal(err)
	}
	first.artifactsMu.RLock()
	defer first.artifactsMu.RUnlock()
	if second.artifactsMu.TryLock() {
		second.artifactsMu.Unlock()
		t.Fatal("root alias bypassed publication/collection exclusion")
	}
}

func TestArtifactCollectionCancellationPreservesCandidates(t *testing.T) {
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts := t.TempDir()
	candidate := filepath.Join(artifacts, "snapshot-"+strings.Repeat("b", 64))
	if err := os.Mkdir(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if removed, err := store.CollectArtifacts(ctx, artifacts); !errors.Is(err, context.Canceled) || removed != 0 {
		t.Fatalf("canceled collection: %d %v", removed, err)
	}
	if _, err := os.Stat(candidate); err != nil {
		t.Fatal("canceled collection removed candidate", err)
	}
}
