package stream


// State represents the state of an operator.
type State map[string]interface{}

// Operator is the base interface for all stream operators.
type Operator interface {
	// ID returns the unique identifier of the operator.
	ID() string
	// Process processes an event and returns the processed event.
	Process(event Event) Event
	// Snapshot snapshots the state of the operator.
	Snapshot(checkpointID int64) State
	// Restore restores the state of the operator.
	Restore(state State)
	// HandleBarrier handles a barrier event.
	HandleBarrier(checkpointID int64)
}
