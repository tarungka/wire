package engine

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
)

// RelocateStateHandles returns an owned copy with selected Pebble handles
// replaced by verified replica locations. File identities must remain exactly
// the same. The receiver must have durably imported those files before calling
// this function; path substitution alone does not establish durability.
func (s TaskCheckpoint) RelocateStateHandles(replacements map[int]SnapshotHandle) (TaskCheckpoint, error) {
	if err := s.ValidateStateHandles(); err != nil {
		return TaskCheckpoint{}, err
	}
	marked := make(map[int]bool, len(s.StateHandleIndexes))
	for _, index := range s.StateHandleIndexes {
		marked[index] = true
	}
	result := s
	result.Source = append([]byte(nil), s.Source...)
	result.Operators = make([][]byte, len(s.Operators))
	for i, data := range s.Operators {
		result.Operators[i] = append([]byte(nil), data...)
	}
	result.StateHandleIndexes = append([]int(nil), s.StateHandleIndexes...)
	for index, replacement := range replacements {
		if !marked[index] {
			return TaskCheckpoint{}, errors.New("replacement does not name a typed snapshot entry")
		}
		data := s.Source
		if index >= 0 {
			data = s.Operators[index]
		}
		var original SnapshotHandle
		if err := json.Unmarshal(data, &original); err != nil {
			return TaskCheckpoint{}, err
		}
		if original.BackendType != StateBackendPebble || replacement.BackendType != original.BackendType || replacement.CheckpointID != original.CheckpointID {
			return TaskCheckpoint{}, errors.New("replacement changes snapshot identity")
		}
		var before, after pebbleSnapshotManifest
		if err := json.Unmarshal(original.Data, &before); err != nil {
			return TaskCheckpoint{}, err
		}
		if err := json.Unmarshal(replacement.Data, &after); err != nil {
			return TaskCheckpoint{}, err
		}
		if before.Version != 1 || after.Version != before.Version || before.CheckpointID != s.CheckpointID || after.CheckpointID != before.CheckpointID || len(before.Files) == 0 || !reflect.DeepEqual(before.Files, after.Files) || !filepath.IsAbs(after.Path) {
			return TaskCheckpoint{}, errors.New("replacement changes snapshot contents or has invalid location")
		}
		encoded, err := json.Marshal(replacement)
		if err != nil {
			return TaskCheckpoint{}, err
		}
		if index == -1 {
			result.Source = encoded
		} else {
			result.Operators[index] = encoded
		}
	}
	return result, nil
}
