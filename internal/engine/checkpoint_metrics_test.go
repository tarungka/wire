package engine

import (
	"testing"
	"time"
)

func TestNoopCheckpointMetrics(t *testing.T) {
	m := NoopCheckpointMetrics()

	// Verify no panics on all method calls.
	m.IncTimeoutTotal()
	m.ObserveAlignmentTime(100 * time.Millisecond)
}
