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
