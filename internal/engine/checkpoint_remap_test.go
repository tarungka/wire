package engine

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCheckpointRemapPreservesTypedStateAndPreparedSink(t *testing.T) {
	handle, err := json.Marshal(SnapshotHandle{CheckpointID: 7, BackendType: StateBackendHashMap})
	if err != nil {
		t.Fatal(err)
	}
	original := TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, HasSource: true, Source: []byte("offset"), Operators: [][]byte{handle, []byte("transaction")}, StateHandleIndexes: []int{0}, SinkPrepared: true, SinkCommittedCheckpoint: 6}
	mapped, err := RemapCheckpointOperators(original, []int{-1, 0, -1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.TaskID != original.TaskID || mapped.CheckpointID != 7 || mapped.EpochID != 2 || !mapped.SinkPrepared || mapped.SinkCommittedCheckpoint != 6 || string(mapped.Source) != "offset" || string(mapped.Operators[3]) != "transaction" || !reflect.DeepEqual(mapped.StateHandleIndexes, []int{1}) {
		t.Fatalf("state identity lost: %+v", mapped)
	}
	if mapped.Operators[0] != nil || mapped.Operators[2] != nil {
		t.Fatal("new operators inherited state")
	}
	if err := mapped.ValidateStateHandles(); err != nil {
		t.Fatal(err)
	}
	mapped.Source[0] = 'x'
	mapped.Operators[3][0] = 'x'
	if string(original.Source) != "offset" || string(original.Operators[1]) != "transaction" {
		t.Fatal("remapping mutated original archive")
	}
	for _, bad := range [][]int{{0}, {1, 0}, {0, 0, 1}, {-2, 0, 1}, {0, 1, -1}, {0, 1, 2}} {
		if _, err := RemapCheckpointOperators(original, bad); err == nil {
			t.Errorf("invalid mapping accepted: %v", bad)
		}
	}
}

func TestCheckpointRemapRejectsHiddenStateOnRemovedTransform(t *testing.T) {
	original := TaskCheckpoint{TaskID: "task", CheckpointID: 9, HasSource: true, Source: []byte("offset"), Operators: [][]byte{nil, nil, []byte("transaction")}, SinkPrepared: true}
	mapped, err := RemapCheckpointOperators(original, []int{0, 2})
	if err != nil || len(mapped.Operators) != 2 || string(mapped.Operators[1]) != "transaction" {
		t.Fatalf("mapped=%+v error=%v", mapped, err)
	}
	original.Operators[1] = []byte("unexpected custom state")
	if _, err := RemapCheckpointOperators(original, []int{0, 2}); err == nil {
		t.Fatal("silently dropped removed operator state")
	}
	if string(original.Operators[1]) != "unexpected custom state" {
		t.Fatal("failed migration mutated archive")
	}
}

func TestCheckpointRemovalMovesTypedHandleIndex(t *testing.T) {
	handle, err := json.Marshal(SnapshotHandle{CheckpointID: 9, BackendType: StateBackendPebble})
	if err != nil {
		t.Fatal(err)
	}
	original := TaskCheckpoint{TaskID: "task", CheckpointID: 9, Operators: [][]byte{nil, nil, handle, []byte("transaction")}, StateHandleIndexes: []int{2}, SinkPrepared: true}
	mapped, err := RemapCheckpointOperators(original, []int{0, 2, 3})
	if err != nil || !reflect.DeepEqual(mapped.StateHandleIndexes, []int{1}) {
		t.Fatalf("mapped=%+v error=%v", mapped, err)
	}
	if err := mapped.ValidateStateHandles(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.StateHandleIndexes, []int{2}) {
		t.Fatal("original typed index changed")
	}
}
