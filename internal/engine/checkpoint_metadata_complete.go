package engine

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// ValidateComplete checks invariants required before publishing or restoring a
// complete manifest. Validate alone accepts partial manifests for inspection.
// A physical task belongs to the head operator of a chain; chained operators
// share its task coverage and key-group ownership.
func (m *CheckpointMetadata) ValidateComplete() error {
	if err := m.Validate(); err != nil {
		return err
	}
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidCheckpointMetadata, fmt.Sprintf(format, args...))
	}
	if m.CheckpointID <= 0 || len(m.JobGraph.Operators) == 0 {
		return invalid("checkpoint identity and graph must be populated")
	}
	operators := make(map[string]OperatorMeta)
	for _, op := range m.JobGraph.Operators {
		operators[op.OperatorID] = op
	}
	for _, op := range m.JobGraph.Operators {
		seen := map[string]bool{op.OperatorID: true}
		current := op
		for current.ChainedTo != nil {
			parent, ok := operators[*current.ChainedTo]
			if !ok || seen[parent.OperatorID] {
				return invalid("operator %q has a missing or cyclic chain parent", op.OperatorID)
			}
			if parent.Parallelism != op.Parallelism {
				return invalid("operator %q has different parallelism from its chain", op.OperatorID)
			}
			seen[parent.OperatorID] = true
			current = parent
		}
	}
	tasks := make(map[string][]TaskMeta)
	paths := make(map[string]bool)
	for _, task := range m.Tasks {
		if operators[task.OperatorID].ChainedTo != nil {
			return invalid("task %q belongs to a chained operator instead of its head", task.TaskID)
		}
		statePath := strings.TrimSuffix(task.StatePath, "/")
		if paths[statePath] {
			return invalid("state path %q is shared by multiple tasks", task.StatePath)
		}
		paths[statePath] = true
		for name, checksum := range task.StateSHA256 {
			declared := false
			for _, file := range task.StateFiles {
				if file == name {
					declared = true
				}
			}
			digest, err := hex.DecodeString(checksum)
			if !declared || err != nil || len(digest) != 32 {
				return invalid("task %q has invalid checksum for %q", task.TaskID, name)
			}
		}
		tasks[task.OperatorID] = append(tasks[task.OperatorID], task)
	}
	for _, op := range m.JobGraph.Operators {
		if op.ChainedTo != nil {
			continue
		}
		owned := tasks[op.OperatorID]
		if len(owned) != op.Parallelism {
			return invalid("operator %q has %d tasks, expected %d", op.OperatorID, len(owned), op.Parallelism)
		}
		sort.Slice(owned, func(i, j int) bool { return owned[i].KeyGroupRange.Start < owned[j].KeyGroupRange.Start })
		end := 0
		for _, task := range owned {
			if task.KeyGroupRange.Start != end {
				return invalid("operator %q has overlapping or missing key groups at %d", op.OperatorID, end)
			}
			end = task.KeyGroupRange.End
		}
		if end != m.JobGraph.NumKeyGroups {
			return invalid("operator %q does not cover all key groups", op.OperatorID)
		}
	}
	return nil
}
