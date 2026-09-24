package engine

import "fmt"

// RemapCheckpointOperators inserts empty state slots without rewriting the
// original checkpoint identity or mutating archive contents. Retained operators
// remain in order, and omitted positions must contain no state or typed handles.
// The prepared sink must remain at the end.
func RemapCheckpointOperators(snapshot TaskCheckpoint, indexes []int) (TaskCheckpoint, error) {
	if err := snapshot.ValidateStateHandles(); err != nil {
		return TaskCheckpoint{}, err
	}
	next := snapshot
	next.Operators = make([][]byte, len(indexes))
	next.StateHandleIndexes = nil
	destinations := make(map[int]int, len(snapshot.Operators))
	last := -1
	for target, source := range indexes {
		if source == -1 {
			continue
		}
		if source <= last || source >= len(snapshot.Operators) {
			return TaskCheckpoint{}, fmt.Errorf("invalid checkpoint operator mapping")
		}
		next.Operators[target] = append([]byte(nil), snapshot.Operators[source]...)
		destinations[source] = target
		last = source
	}
	for source, data := range snapshot.Operators {
		if _, retained := destinations[source]; !retained && len(data) != 0 {
			return TaskCheckpoint{}, fmt.Errorf("checkpoint mapping drops nonempty operator state")
		}
	}
	for _, source := range snapshot.StateHandleIndexes {
		if source >= 0 {
			if _, retained := destinations[source]; !retained {
				return TaskCheckpoint{}, fmt.Errorf("checkpoint mapping drops a typed state handle")
			}
		}
	}
	if snapshot.SinkPrepared && (len(snapshot.Operators) == 0 || len(indexes) == 0 || indexes[len(indexes)-1] != len(snapshot.Operators)-1) {
		return TaskCheckpoint{}, fmt.Errorf("checkpoint mapping moves prepared sink away from task end")
	}
	next.Source = append([]byte(nil), snapshot.Source...)
	for _, source := range snapshot.StateHandleIndexes {
		if source == -1 {
			next.StateHandleIndexes = append(next.StateHandleIndexes, -1)
		} else {
			next.StateHandleIndexes = append(next.StateHandleIndexes, destinations[source])
		}
	}
	return next, nil
}
