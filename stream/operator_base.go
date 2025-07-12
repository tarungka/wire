package stream


// BaseOperator is a base struct for all stream operators.
type BaseOperator struct {
	// The unique identifier of the operator.
	id string
	// The state of the operator.
	state State
}

// NewBaseOperator creates a new BaseOperator.
func NewBaseOperator(id string) *BaseOperator {
	return &BaseOperator{
		id:    id,
		state: make(State),
	}
}

// ID returns the unique identifier of the operator.
func (o *BaseOperator) ID() string {
	return o.id
}

// Process processes an event.
func (o *BaseOperator) Process(event Event) Event {
	// This method should be implemented by the specific operator.
	return event
}

// Snapshot snapshots the state of the operator.
func (o *BaseOperator) Snapshot(checkpointID int64) State {
	return o.state
}

// Restore restores the state of the operator.
func (o *BaseOperator) Restore(state State) {
	o.state = state
}

// HandleBarrier handles a barrier event.
func (o *BaseOperator) HandleBarrier(checkpointID int64) {
	// This method should be implemented by the specific operator.
}
