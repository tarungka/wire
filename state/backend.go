package state

import "github.com/tarungka/wire/stream"

// StateBackend is an interface for storing and retrieving operator state.
type StateBackend interface {
	// Save saves the state of an operator.
	Save(operatorID string, checkpointID int64, state stream.State) error
	// Load loads the state of an operator.
	Load(operatorID string, checkpointID int64) (stream.State, error)
}
