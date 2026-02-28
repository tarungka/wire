package engine

import "time"

// CheckpointMetrics abstracts checkpoint-related metrics collection.
// Implementations may bridge to Prometheus or other systems.
type CheckpointMetrics interface {
	IncTimeoutTotal()
	ObserveAlignmentTime(d time.Duration)
	SetAlignmentBufferedBytes(bytes int64)
}

// noopCheckpointMetrics is a no-op implementation for use when no metrics
// system is configured.
type noopCheckpointMetrics struct{}

func (noopCheckpointMetrics) IncTimeoutTotal()                     {}
func (noopCheckpointMetrics) ObserveAlignmentTime(_ time.Duration) {}
func (noopCheckpointMetrics) SetAlignmentBufferedBytes(_ int64)    {}

// NoopCheckpointMetrics returns a CheckpointMetrics that discards all observations.
func NoopCheckpointMetrics() CheckpointMetrics {
	return noopCheckpointMetrics{}
}
