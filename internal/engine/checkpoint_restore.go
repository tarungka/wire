package engine

import (
	"encoding/json"
	"fmt"
)

func (ts *TaskSlot) restoreCheckpoint() error {
	snapshot := ts.RestoreCheckpoint
	if snapshot.TaskID != ts.TaskID || snapshot.CheckpointID == 0 || snapshot.HasSource != (ts.Source != nil) || len(snapshot.Operators) != len(ts.Operators) {
		return fmt.Errorf("restored checkpoint does not match task topology")
	}
	if err := snapshot.ValidateStateHandles(); err != nil {
		return err
	}
	typed := make(map[int]bool, len(snapshot.StateHandleIndexes))
	for _, index := range snapshot.StateHandleIndexes {
		typed[index] = true
	}
	restore := func(index int, operator Operator, data []byte) error {
		owned := append([]byte(nil), data...)
		return invokeOperator(func() error {
			if typed[index] {
				target, ok := operator.(StateHandleOperator)
				if !ok {
					return fmt.Errorf("operator %d cannot restore typed state", index)
				}
				var handle SnapshotHandle
				if err := json.Unmarshal(owned, &handle); err != nil {
					return err
				}
				return target.RestoreState(handle)
			}
			if target, ok := operator.(CheckpointRestorer); ok {
				return target.RestoreCheckpoint(owned)
			}
			if len(owned) != 0 {
				return fmt.Errorf("operator %d cannot restore nonempty checkpoint state", index)
			}
			return nil
		})
	}
	if ts.Source != nil {
		if err := restore(-1, ts.Source, snapshot.Source); err != nil {
			return err
		}
	}
	for index, operator := range ts.Operators {
		if err := restore(index, operator, snapshot.Operators[index]); err != nil {
			return err
		}
	}
	ts.RestoredCheckpointID = snapshot.CheckpointID
	return nil
}
