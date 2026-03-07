package engine

import (
	"fmt"
	"strings"
)

// isStatefulOperator returns true for operator types that maintain keyed or
// offset state. Stateful operators must be present in both savepoint and new
// graph for compatibility.
func isStatefulOperator(opType string) bool {
	switch opType {
	case "source", "keyby", "window":
		return true
	default:
		return false
	}
}

// ValidateSavepointCompatibility checks whether a savepoint's job graph is
// compatible with a new job graph for restore purposes. It collects all
// validation errors before returning so callers see the full picture.
//
// Rules (per WIP-06 Section 3.3):
//  1. num_key_groups must match exactly.
//  2. All stateful operator IDs from savepoint must exist in new graph.
//  3. All stateful operator IDs in new graph must exist in savepoint.
//  4. Operator types must not change for existing IDs.
//  5. New stateless operators may be added (ok).
//  6. Stateless operators may be removed (ok).
func ValidateSavepointCompatibility(savepoint, newGraph *CheckpointMetadata) error {
	var errs []string

	// Rule 1: num_key_groups must match.
	if savepoint.JobGraph.NumKeyGroups != newGraph.JobGraph.NumKeyGroups {
		errs = append(errs, fmt.Sprintf(
			"num_key_groups mismatch: savepoint has %d, new graph has %d",
			savepoint.JobGraph.NumKeyGroups, newGraph.JobGraph.NumKeyGroups))
	}

	// Index operators by ID for lookup.
	savepointOps := make(map[string]OperatorMeta, len(savepoint.JobGraph.Operators))
	for _, op := range savepoint.JobGraph.Operators {
		savepointOps[op.OperatorID] = op
	}

	newOps := make(map[string]OperatorMeta, len(newGraph.JobGraph.Operators))
	for _, op := range newGraph.JobGraph.Operators {
		newOps[op.OperatorID] = op
	}

	// Rule 2: All stateful operators from savepoint must exist in new graph.
	for id, op := range savepointOps {
		if !isStatefulOperator(op.Type) {
			continue
		}
		if _, ok := newOps[id]; !ok {
			errs = append(errs, fmt.Sprintf(
				"stateful operator %q (type %s) in savepoint not found in new graph",
				id, op.Type))
		}
	}

	// Rule 3: All stateful operators in new graph must exist in savepoint.
	for id, op := range newOps {
		if !isStatefulOperator(op.Type) {
			continue
		}
		if _, ok := savepointOps[id]; !ok {
			errs = append(errs, fmt.Sprintf(
				"stateful operator %q (type %s) in new graph not found in savepoint",
				id, op.Type))
		}
	}

	// Rule 4: Operator types must not change for existing IDs.
	for id, newOp := range newOps {
		spOp, ok := savepointOps[id]
		if !ok {
			continue
		}
		if spOp.Type != newOp.Type {
			errs = append(errs, fmt.Sprintf(
				"operator %q type changed: savepoint has %q, new graph has %q",
				id, spOp.Type, newOp.Type))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrSavepointIncompatible, strings.Join(errs, "; "))
}
