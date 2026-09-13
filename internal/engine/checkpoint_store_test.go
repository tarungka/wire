package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
)

func TestCheckpointStoreReopenAndIdempotence(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := TaskCheckpoint{TaskID: "../task", CheckpointID: 7, EpochID: 2, Operators: [][]byte{[]byte("state"), nil}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Put(context.Background(), "../job", snapshot); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reopened, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(context.Background(), "../job", snapshot.TaskID, 7, 2)
	if err != nil || !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("reopened: %+v %v", got, err)
	}
	changed := snapshot
	changed.Operators = [][]byte{[]byte("different")}
	if err := store.Put(context.Background(), "../job", changed); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := store.Get(context.Background(), "other-job", snapshot.TaskID, 7, 2); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job isolation: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files leaked: %d %v", len(entries), err)
	}
}

func TestCheckpointStoreConcurrentConflict(t *testing.T) {
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, value := range []string{"first", "second"} {
		go func() {
			<-start
			results <- store.Put(context.Background(), "job", TaskCheckpoint{
				TaskID: "task", CheckpointID: 1, Operators: [][]byte{[]byte(value)},
			})
		}()
	}
	close(start)
	var successes, conflicts int
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrCheckpointConflict):
			conflicts++
		default:
			t.Errorf("publish: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	got, err := store.Get(context.Background(), "job", "task", 1, 0)
	if err != nil || len(got.Operators) != 1 {
		t.Fatalf("read winner: %+v %v", got, err)
	}
}

func TestCheckpointStoreRejectsInvalidEnvelope(t *testing.T) {
	for _, mutation := range []string{"magic", "length", "truncated", "oversize"} {
		t.Run(mutation, func(t *testing.T) {
			store, err := NewFileCheckpointStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path, err := store.path("job", "task", 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			header := make([]byte, checkpointFileHeaderSize)
			copy(header, "WCP1")
			switch mutation {
			case "magic":
				header[0] = 'X'
			case "length":
				binary.BigEndian.PutUint64(header[4:12], ^uint64(0))
			case "truncated":
				header = header[:12]
			}
			if err := os.WriteFile(path, header, 0600); err != nil {
				t.Fatal(err)
			}
			if mutation == "oversize" {
				if err := os.Truncate(path, maxStoredCheckpointBytes+checkpointFileHeaderSize+1); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Get(context.Background(), "job", "task", 1, 0); !errors.Is(err, ErrCheckpointFileCorrupt) {
				t.Fatalf("invalid envelope accepted: %v", err)
			}
		})
	}
}

func TestCheckpointStoreRejectsCorruptionAndCancellation(t *testing.T) {
	store, err := NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := TaskCheckpoint{TaskID: "task", CheckpointID: 1, Operators: [][]byte{[]byte("state")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Put(ctx, "job", snapshot); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "job", snapshot); err != nil {
		t.Fatal(err)
	}
	path, _ := store.path("job", "task", 1, 0)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents[len(contents)-1] ^= 1
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "job", "task", 1, 0); !errors.Is(err, ErrCheckpointFileCorrupt) {
		t.Fatalf("corruption: %v", err)
	}
	if err := store.Put(context.Background(), "job", snapshot); !errors.Is(err, ErrCheckpointFileCorrupt) {
		t.Fatalf("corrupt existing checkpoint silently replaced: %v", err)
	}
}
