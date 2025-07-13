package engine

import (
	"context"
	"time"

	"github.com/tarungka/wire/stream"
)

// CheckpointCoordinator is responsible for coordinating checkpoints.
type CheckpointCoordinator struct {
	ticker *time.Ticker
	stream chan<- stream.Event
}

// NewCheckpointCoordinator creates a new CheckpointCoordinator.
func NewCheckpointCoordinator(interval time.Duration, stream chan<- stream.Event) *CheckpointCoordinator {
	return &CheckpointCoordinator{
		ticker: time.NewTicker(interval),
		stream: stream,
	}
}

// Start starts the checkpoint coordinator.
func (c *CheckpointCoordinator) Start(ctx context.Context) {
	for {
		select {
		case <-c.ticker.C:
			// Trigger a new checkpoint.
			c.stream <- stream.Barrier{CheckpointID: time.Now().UnixNano()}
		case <-ctx.Done():
			c.ticker.Stop()
			return
		}
	}
}

// handleBarrier handles a barrier event.
func (c *CheckpointCoordinator) handleBarrier(barrier stream.Barrier) {
	// This will be implemented in a future step.
}
