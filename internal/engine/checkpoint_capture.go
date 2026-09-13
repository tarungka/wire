package engine

import (
	"encoding/json"
	"fmt"
)

func captureOperatorCheckpoint(operator Operator, id uint64) ([]byte, bool, error) {
	typed, ok := operator.(StateHandleOperator)
	if !ok {
		data, err := operator.Checkpoint(id)
		return data, false, err
	}
	handle, err := typed.CheckpointState(id)
	if err != nil {
		return nil, true, err
	}
	if handle.CheckpointID != id || (handle.BackendType != StateBackendPebble && handle.BackendType != StateBackendHashMap) {
		return nil, true, fmt.Errorf("invalid typed checkpoint handle")
	}
	data, err := json.Marshal(handle)
	return data, true, err
}

// ValidateStateHandles verifies explicit typed entries without guessing whether
// arbitrary operator bytes happen to resemble a backend handle. Index -1 names
// the source; nonnegative indexes name the ordered operator snapshot entries.
func (s TaskCheckpoint) ValidateStateHandles() error {
	seen := make(map[int]bool)
	for _, index := range s.StateHandleIndexes {
		if seen[index] || index < -1 || index >= len(s.Operators) || (index == -1 && !s.HasSource) {
			return fmt.Errorf("invalid checkpoint state-handle index %d", index)
		}
		seen[index] = true
		data := s.Source
		if index >= 0 {
			data = s.Operators[index]
		}
		var handle SnapshotHandle
		if err := json.Unmarshal(data, &handle); err != nil {
			return err
		}
		if handle.CheckpointID != s.CheckpointID || (handle.BackendType != StateBackendPebble && handle.BackendType != StateBackendHashMap) {
			return fmt.Errorf("invalid typed checkpoint handle at index %d", index)
		}
	}
	return nil
}
