package engine

import (
	"errors"
	"strings"
	"testing"
)

// baseGraph returns a job graph metadata with a standard mix of stateful and
// stateless operators for use in compatibility tests.
func baseGraph() *CheckpointMetadata {
	return &CheckpointMetadata{
		SchemaVersion: CurrentSchemaVersion,
		Type:          SavepointType,
		JobGraph: JobGraphMeta{
			NumKeyGroups: 128,
			Operators: []OperatorMeta{
				{OperatorID: "source-1", Type: "source", Parallelism: 2},
				{OperatorID: "map-1", Type: "map", Parallelism: 4},
				{OperatorID: "filter-1", Type: "filter", Parallelism: 4},
				{OperatorID: "keyby-1", Type: "keyby", Parallelism: 4},
				{OperatorID: "window-1", Type: "window", Parallelism: 4},
				{OperatorID: "sink-1", Type: "sink", Parallelism: 2},
			},
		},
	}
}

func TestValidateSavepointCompatibility_Compatible(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()

	if err := ValidateSavepointCompatibility(savepoint, newGraph); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateSavepointCompatibility_DifferentParallelism(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	newGraph.JobGraph.Operators[3].Parallelism = 8 // keyby-1: 4 → 8

	if err := ValidateSavepointCompatibility(savepoint, newGraph); err != nil {
		t.Errorf("parallelism change should be allowed, got %v", err)
	}
}

func TestValidateSavepointCompatibility_KeyGroupMismatch(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	newGraph.JobGraph.NumKeyGroups = 256

	err := ValidateSavepointCompatibility(savepoint, newGraph)
	if err == nil {
		t.Fatal("expected error for key group mismatch")
	}
	if !strings.Contains(err.Error(), "num_key_groups mismatch") {
		t.Errorf("error = %v, want message about num_key_groups", err)
	}
}

func TestValidateSavepointCompatibility_MissingStatefulOperator(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	// Remove window-1 from new graph.
	newGraph.JobGraph.Operators = []OperatorMeta{
		{OperatorID: "source-1", Type: "source", Parallelism: 2},
		{OperatorID: "map-1", Type: "map", Parallelism: 4},
		{OperatorID: "filter-1", Type: "filter", Parallelism: 4},
		{OperatorID: "keyby-1", Type: "keyby", Parallelism: 4},
		{OperatorID: "sink-1", Type: "sink", Parallelism: 2},
	}

	err := ValidateSavepointCompatibility(savepoint, newGraph)
	if err == nil {
		t.Fatal("expected error for missing stateful operator")
	}
	if !strings.Contains(err.Error(), "window-1") {
		t.Errorf("error = %v, want message about window-1", err)
	}
}

func TestValidateSavepointCompatibility_NewStatefulOperator(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	// Add new keyby operator to new graph.
	newGraph.JobGraph.Operators = append(newGraph.JobGraph.Operators,
		OperatorMeta{OperatorID: "keyby-2", Type: "keyby", Parallelism: 2})

	err := ValidateSavepointCompatibility(savepoint, newGraph)
	if err == nil {
		t.Fatal("expected error for new stateful operator")
	}
	if !strings.Contains(err.Error(), "keyby-2") {
		t.Errorf("error = %v, want message about keyby-2", err)
	}
}

func TestValidateSavepointCompatibility_TypeChanged(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	// Change source-1 type from "source" to "keyby".
	newGraph.JobGraph.Operators[0].Type = "keyby"

	err := ValidateSavepointCompatibility(savepoint, newGraph)
	if err == nil {
		t.Fatal("expected error for type change")
	}
	if !strings.Contains(err.Error(), "type changed") {
		t.Errorf("error = %v, want message about type change", err)
	}
}

func TestValidateSavepointCompatibility_AddStatelessOperator(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	// Add a new stateless map operator.
	newGraph.JobGraph.Operators = append(newGraph.JobGraph.Operators,
		OperatorMeta{OperatorID: "map-2", Type: "map", Parallelism: 2})

	if err := ValidateSavepointCompatibility(savepoint, newGraph); err != nil {
		t.Errorf("adding stateless operator should be allowed, got %v", err)
	}
}

func TestValidateSavepointCompatibility_RemoveStatelessOperator(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	// Remove filter-1 (stateless) from new graph.
	newGraph.JobGraph.Operators = []OperatorMeta{
		{OperatorID: "source-1", Type: "source", Parallelism: 2},
		{OperatorID: "map-1", Type: "map", Parallelism: 4},
		{OperatorID: "keyby-1", Type: "keyby", Parallelism: 4},
		{OperatorID: "window-1", Type: "window", Parallelism: 4},
		{OperatorID: "sink-1", Type: "sink", Parallelism: 2},
	}

	if err := ValidateSavepointCompatibility(savepoint, newGraph); err != nil {
		t.Errorf("removing stateless operator should be allowed, got %v", err)
	}
}

func TestValidateSavepointCompatibility_MultipleErrors(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	// Trigger multiple errors: key group mismatch + remove stateful operator.
	newGraph.JobGraph.NumKeyGroups = 256
	newGraph.JobGraph.Operators = []OperatorMeta{
		{OperatorID: "source-1", Type: "source", Parallelism: 2},
		{OperatorID: "map-1", Type: "map", Parallelism: 4},
		{OperatorID: "filter-1", Type: "filter", Parallelism: 4},
		{OperatorID: "keyby-1", Type: "keyby", Parallelism: 4},
		// window-1 removed
		{OperatorID: "sink-1", Type: "sink", Parallelism: 2},
	}

	err := ValidateSavepointCompatibility(savepoint, newGraph)
	if err == nil {
		t.Fatal("expected error for multiple violations")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "num_key_groups") {
		t.Errorf("error should mention num_key_groups: %v", err)
	}
	if !strings.Contains(errMsg, "window-1") {
		t.Errorf("error should mention window-1: %v", err)
	}
}

func TestValidateSavepointCompatibility_ErrorIs(t *testing.T) {
	savepoint := baseGraph()
	newGraph := baseGraph()
	newGraph.JobGraph.NumKeyGroups = 256

	err := ValidateSavepointCompatibility(savepoint, newGraph)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrSavepointIncompatible) {
		t.Errorf("errors.Is(err, ErrSavepointIncompatible) = false, want true; err = %v", err)
	}
}

func TestIsStatefulOperator(t *testing.T) {
	tests := []struct {
		opType string
		want   bool
	}{
		{"source", true},
		{"keyby", true},
		{"window", true},
		{"map", false},
		{"filter", false},
		{"sink", false},
		{"flatmap", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.opType, func(t *testing.T) {
			if got := isStatefulOperator(tt.opType); got != tt.want {
				t.Errorf("isStatefulOperator(%q) = %v, want %v", tt.opType, got, tt.want)
			}
		})
	}
}
