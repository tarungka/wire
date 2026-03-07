package engine

// DLQEvent represents a poisoned event routed to the Dead Letter Queue.
// It wraps the original event with diagnostic metadata.
type DLQEvent struct {
	OriginalEvent Event
	Error         string
	OperatorName  string
	Timestamp     int64 // Unix milliseconds.
	RetryCount    int
}
