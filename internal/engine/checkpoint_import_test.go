package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCheckpointImportIdentityAndPublication(t *testing.T) {
	ctx := context.Background()
	snapshot := TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, HasSource: true, Source: []byte("offset"), Operators: [][]byte{[]byte("operator")}}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(append([]byte(nil), payload...), []byte(" {}")...), []byte(`{"TaskID":"task","CheckpointID":7,"EpochID":2,"unknown":true}`), []byte(`{"TaskID":"other","CheckpointID":7,"EpochID":2}`)} {
		if err := store.Import(ctx, "job", "task", 7, 2, bytes.NewReader(bad)); err == nil {
			t.Fatal("invalid import published")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid import touched storage: %v %v", entries, err)
	}
	if err := store.Import(ctx, "job", "task", 7, 2, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(ctx, "job", "task", 7, 2)
	if err != nil || !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("import recovery: %+v %v", got, err)
	}
}
