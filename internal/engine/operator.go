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

// TransactionalSink extends SinkOperator with two-phase commit (2PC) support.
// Sinks that implement this interface participate in the checkpoint protocol:
//   - BeginTransaction: open a new transaction (called at startup and after each Commit/Abort)
//   - PreCommit: flush buffered data and prepare the transaction for commit
//   - Commit: finalize the transaction after global checkpoint completion
//   - Abort: rollback the in-flight transaction on failure
type TransactionalSink interface {
	SinkOperator
	BeginTransaction(ctx context.Context) error
	PreCommit(ctx context.Context, checkpointID uint64) error
	Commit(ctx context.Context, checkpointID uint64) error
	Abort(ctx context.Context) error
}
