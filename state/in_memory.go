package state

import (
	"fmt"
	"sync"

	"github.com/tarungka/wire/stream"
)

// InMemoryStateBackend is an in-memory implementation of the StateBackend interface.
type InMemoryStateBackend struct {
	mu    sync.RWMutex
	state map[string]stream.State
}

// NewInMemoryStateBackend creates a new InMemoryStateBackend.
func NewInMemoryStateBackend() *InMemoryStateBackend {
	return &InMemoryStateBackend{
		state: make(map[string]stream.State),
	}
}

// Save saves the state of an operator.
func (b *InMemoryStateBackend) Save(operatorID string, checkpointID int64, state stream.State) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := fmt.Sprintf("%s-%d", operatorID, checkpointID)
	b.state[key] = state
	return nil
}

// Load loads the state of an operator.
func (b *InMemoryStateBackend) Load(operatorID string, checkpointID int64) (stream.State, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	key := fmt.Sprintf("%s-%d", operatorID, checkpointID)
	state, ok := b.state[key]
	if !ok {
		return nil, fmt.Errorf("state not found for operator %s and checkpoint %d", operatorID, checkpointID)
	}
	return state, nil
}
