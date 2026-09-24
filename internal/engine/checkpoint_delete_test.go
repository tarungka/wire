package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
)

func TestCheckpointDeletionFencesLateWritesAndSurvivesReopen(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := TaskCheckpoint{TaskID: "task", CheckpointID: 1, EpochID: 2}
	if err := store.Put(t.Context(), "job", snapshot); err != nil {
		t.Fatal(err)
	}
	path, err := store.path("job", "task", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".archive", []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), "job", "task", 1, 2); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Delete(t.Context(), "job", "task", 1, 2); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".archive"} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("payload remains: %s %v", name, err)
		}
	}
	if err := reopened.Put(t.Context(), "job", snapshot); !errors.Is(err, ErrCheckpointDeleted) {
		t.Fatalf("late upload: %v", err)
	}
	if _, err := reopened.Get(t.Context(), "job", "task", 1, 2); !errors.Is(err, ErrCheckpointDeleted) {
		t.Fatalf("deleted read: %v", err)
	}
	if _, err := reopened.OpenArchive(t.Context(), "job", "task", 1, 2); !errors.Is(err, ErrCheckpointDeleted) {
		t.Fatalf("deleted archive: %v", err)
	}
	snapshot.EpochID = 3
	if err := reopened.Put(t.Context(), "job", snapshot); err != nil {
		t.Fatal("unrelated epoch fenced", err)
	}
}

func TestCheckpointDeletionRacesPublication(t *testing.T) {
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for id := uint64(1); id <= 30; id++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			err := store.Put(t.Context(), "job", TaskCheckpoint{TaskID: "task", CheckpointID: id, EpochID: 1})
			if err != nil && !errors.Is(err, ErrCheckpointDeleted) && !errors.Is(err, os.ErrNotExist) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := store.Delete(t.Context(), "job", "task", id, 1); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		if _, err := store.Get(t.Context(), "job", "task", id, 1); !errors.Is(err, ErrCheckpointDeleted) {
			t.Fatalf("read after race: %v", err)
		}
		path, _ := store.path("job", "task", id, 1)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("resurrected payload: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Delete(ctx, "job", "task", 99, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type heldCheckpointReader struct {
	reader  *bytes.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *heldCheckpointReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.release })
	return r.reader.Read(p)
}

func TestCheckpointDeletionDuringArchiveUpload(t *testing.T) {
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	snapshot := TaskCheckpoint{TaskID: "task", CheckpointID: 1, EpochID: 2}
	if err := ExportTaskCheckpoint(t.Context(), snapshot, &archive, t.TempDir(), 1<<20); err != nil {
		t.Fatal(err)
	}
	reader := &heldCheckpointReader{reader: bytes.NewReader(archive.Bytes()), started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	artifacts := t.TempDir()
	go func() { done <- store.ImportArchive(t.Context(), "job", "task", 1, 2, reader, artifacts, 1<<20) }()
	<-reader.started
	reopened, err := NewFileCheckpointStore(store.root)
	if err != nil {
		close(reader.release)
		<-done
		t.Fatal(err)
	}
	if reopened.artifactsMu.TryLock() {
		reopened.artifactsMu.Unlock()
		close(reader.release)
		<-done
		t.Fatal("artifact collection can overlap an unfinished import")
	}
	deleteErr := store.Delete(t.Context(), "job", "task", 1, 2)
	close(reader.release)
	uploadErr := <-done
	if deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if !errors.Is(uploadErr, ErrCheckpointDeleted) {
		t.Fatalf("late archive upload: %v", uploadErr)
	}
	path, _ := store.path("job", "task", 1, 2)
	for _, name := range []string{path, path + ".archive"} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("resurrected %s: %v", name, err)
		}
	}
}

func TestCheckpointDeletionRetriesInterruptedRemoval(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := TaskCheckpoint{TaskID: "task", CheckpointID: 1, EpochID: 2}
	if err := store.Put(t.Context(), "job", snapshot); err != nil {
		t.Fatal(err)
	}
	path, err := store.path("job", "task", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after the durable fence but before payload removal.
	if err := os.WriteFile(path+".deleted", nil, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(t.Context(), "job", "task", 1, 2); !errors.Is(err, ErrCheckpointDeleted) {
		t.Fatalf("interrupted deletion became readable: %v", err)
	}
	if err := reopened.Delete(t.Context(), "job", "task", 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retry retained payload: %v", err)
	}
}
