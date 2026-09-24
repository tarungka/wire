package engine

import "fmt"

// RemapCheckpointOperators inserts empty state slots without rewriting the
// original checkpoint identity or mutating archive contents. Every old operator
// must be retained exactly once and in order, including the prepared sink.
func RemapCheckpointOperators(snapshot TaskCheckpoint, indexes []int) (TaskCheckpoint, error) {
	if err := snapshot.ValidateStateHandles(); err != nil {
		return TaskCheckpoint{}, err
	}
	next := snapshot
	next.Operators = make([][]byte, len(indexes))
	next.StateHandleIndexes = nil
	destinations := make(map[int]int, len(snapshot.Operators))
	expected := 0
	for target, source := range indexes {
		if source == -1 {
			continue
		}
		if source != expected || source >= len(snapshot.Operators) {
			return TaskCheckpoint{}, fmt.Errorf("invalid checkpoint operator mapping")
		}
		next.Operators[target] = append([]byte(nil), snapshot.Operators[source]...)
		destinations[source] = target
		expected++
	}
	if expected != len(snapshot.Operators) {
		return TaskCheckpoint{}, fmt.Errorf("checkpoint mapping drops saved operators")
	}
	if snapshot.SinkPrepared && (len(indexes) == 0 || indexes[len(indexes)-1] != len(snapshot.Operators)-1) {
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
