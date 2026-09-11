package sdk

import "context"

// Sink consumes events as a terminal operator.
type Sink interface {
	// Open initializes the sink (e.g. connect to database, open file).
	Open(ctx context.Context) error
	// Write writes a single event to the sink.
	Write(ctx context.Context, event Event) error
	// Close releases resources held by the sink.
	Close() error
}

// BatchSink optionally accepts an explicit batch. Write remains synchronous;
// callers must handle partial delivery if a batch spans multiple requests.
type BatchSink interface {
	Sink
	WriteBatch(ctx context.Context, events []Event) error
}
