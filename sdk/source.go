package sdk

import "context"

// Source produces events from an external system.
type Source interface {
	// Open initializes the source (e.g. connect to broker, open file).
	Open(ctx context.Context) error
	// ReadBatch returns the next batch of events. Returns nil slice at end of input.
	ReadBatch(ctx context.Context) ([]Event, error)
	// Close releases resources held by the source.
	Close() error
	// GenerateWatermark returns the current watermark timestamp (millis).
	// Must be safe for concurrent use.
	GenerateWatermark() int64
}
