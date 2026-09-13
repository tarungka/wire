package engine

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCheckpointRelocationPreservesEntryIdentity(t *testing.T) {
	manifest := pebbleSnapshotManifest{Version: 1, CheckpointID: 7, Path: filepath.Join(t.TempDir(), "original"), Files: map[string]string{"file": "hash"}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	original := SnapshotHandle{CheckpointID: 7, BackendType: StateBackendPebble, Data: data}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, HasSource: true, Source: encoded, Operators: [][]byte{[]byte("opaque"), encoded}, StateHandleIndexes: []int{-1, 1}}
	manifest.Path = filepath.Join(t.TempDir(), "replica")
	replacement := original
	replacement.Data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	result, err := checkpoint.RelocateStateHandles(map[int]SnapshotHandle{-1: replacement, 1: replacement})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Operators[0]) != "opaque" || string(checkpoint.Source) != string(encoded) || string(checkpoint.Operators[1]) != string(encoded) {
		t.Fatal("relocation altered unrelated or original state")
	}
	var got SnapshotHandle
	if err := json.Unmarshal(result.Source, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != string(replacement.Data) {
		t.Fatal("source was not relocated")
	}
	if _, err := checkpoint.RelocateStateHandles(map[int]SnapshotHandle{0: replacement}); err == nil {
		t.Fatal("unmarked entry replaced")
	}
	replacement.CheckpointID++
	if _, err := checkpoint.RelocateStateHandles(map[int]SnapshotHandle{1: replacement}); err == nil {
		t.Fatal("checkpoint identity changed")
	}
	replacement.CheckpointID--
	manifest.Files["file"] = "different"
	replacement.Data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.RelocateStateHandles(map[int]SnapshotHandle{1: replacement}); err == nil {
		t.Fatal("artifact contents changed")
	}
}
