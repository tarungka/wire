package checkpoint

import (
	"time"

	"github.com/tarungka/wire/stream"
	"github.com/tarungka/wire/state"
)

// Checkpoint represents a checkpoint.
type Checkpoint struct {
	ID        int64
	Timestamp time.Time
	// OperatorData stores the location of the state for each operator.
	OperatorData map[string]string
}

// CheckpointManager is responsible for creating and restoring checkpoints.
type CheckpointManager struct {
	backend state.StateBackend
}

// NewCheckpointManager creates a new CheckpointManager.
func NewCheckpointManager(backend state.StateBackend) *CheckpointManager {
	return &CheckpointManager{
		backend: backend,
	}
}

// CreateCheckpoint creates a new checkpoint.
func (c *CheckpointManager) CreateCheckpoint(operators []stream.Operator) (*Checkpoint, error) {
	checkpointID := time.Now().UnixNano()
	checkpoint := &Checkpoint{
		ID:        checkpointID,
		Timestamp: time.Now(),
	}

	for _, operator := range operators {
		state := operator.Snapshot(checkpointID)
		if err := c.backend.Save(operator.ID(), checkpointID, state); err != nil {
			return nil, err
		}
	}

	return checkpoint, nil
}

// RestoreCheckpoint restores a checkpoint.
func (c *CheckpointManager) RestoreCheckpoint(checkpoint *Checkpoint, operators []stream.Operator) error {
	for _, operator := range operators {
		state, err := c.backend.Load(operator.ID(), checkpoint.ID)
		if err != nil {
			return err
		}
		operator.Restore(state)
	}
	return nil
}

// Add an ID() method to the Operator interface in stream/operator.go
func (o *stream.BaseOperator) ID() string {
	// This should be a unique identifier for the operator.
	// For now, we'll use a placeholder.
	return "operator-id"
}
