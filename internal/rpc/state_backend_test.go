package rpc

import "testing"

func TestStateBackendValidation(t *testing.T) {
	for _, spec := range []*StateBackendSpec{nil, {Type: "hashmap", MaxMemoryBytes: 1024}, {Type: "pebble", MaxCompactionConcurrency: 2}, {}} {
		if err := (OperatorDescriptor{Type: OperatorTypeProcess, StateBackend: spec}).ValidateStateBackend(); err != nil {
			t.Fatal(err)
		}
	}
	for _, op := range []OperatorDescriptor{
		{Type: OperatorTypeMap, StateBackend: &StateBackendSpec{Type: "hashmap"}},
		{Type: OperatorTypeProcess, StateBackend: &StateBackendSpec{Type: "unknown"}},
		{Type: OperatorTypeWindow, StateBackend: &StateBackendSpec{MaxMemoryBytes: -1}},
		{Type: OperatorTypeWindow, StateBackend: &StateBackendSpec{MaxCompactionConcurrency: -1}},
	} {
		if op.ValidateStateBackend() == nil {
			t.Fatalf("invalid configuration accepted: %+v", op)
		}
	}
}
