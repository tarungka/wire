package engine

import "context"

// Operator is the base interface for all stream processing operators.
type Operator interface {
	// Open initializes the operator. Called once before processing starts.
	Open(ctx context.Context) error
	// Close releases resources. Called once after processing ends.
	Close() error
	// Checkpoint snapshots the operator state for the given checkpoint ID.
	Checkpoint(checkpointID uint64) ([]byte, error)
}

// MapOperator transforms each input event into exactly one output event.
type MapOperator interface {
	Operator
	Map(ctx context.Context, event Event) (Event, error)
}

// FlatMapOperator transforms each input event into zero or more output events.
type FlatMapOperator interface {
	Operator
	FlatMap(ctx context.Context, event Event, emit func(Event)) error
}

// SourceOperator produces events from an external source.
type SourceOperator interface {
	Operator
	// ReadBatch returns the next batch of events. Returns nil slice at end of input.
	ReadBatch(ctx context.Context) ([]Event, error)
	// GenerateWatermark returns the current watermark timestamp.
	// Must be thread-safe (called from the watermark emitter goroutine).
	GenerateWatermark() int64
}

// SinkOperator consumes events as a terminal operator.
type SinkOperator interface {
	Operator
	Write(ctx context.Context, event Event) error
}
