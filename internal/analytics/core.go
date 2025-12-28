package analytics

import (
	"context"
	"time"
)

// Record represents a single data element flowing through the pipeline.
type Record struct {
	Timestamp    time.Time
	Data         interface{}
	Metadata     map[string]interface{}
	CheckpointID uint64 // 0 if not a barrier
}

// NewRecord creates a new record with the current timestamp.
func NewRecord(data interface{}) *Record {
	return &Record{
		Timestamp: time.Now(),
		Data:      data,
		Metadata:  make(map[string]interface{}),
	}
}

// NewBarrier creates a new checkpoint barrier record.
func NewBarrier(id uint64) *Record {
	return &Record{
		Timestamp:    time.Now(),
		CheckpointID: id,
	}
}

func (r *Record) IsBarrier() bool {
	return r.CheckpointID > 0
}

// Operator is the interface for all processing nodes in the DAG.
type Operator interface {
	// ID returns the unique identifier for this operator instance.
	ID() string

	// Open initializes the operator.
	Open(ctx OperatorContext) error

	// ProcessElement handles a single record.
	ProcessElement(record *Record, out Stream) error

	// Close performs cleanup.
	Close() error
}

// Stream is the interface for passing records between operators.
// It handles backpressure and routing.
type Stream interface {
	// Emit sends a record to the next operator(s).
	Emit(record *Record) error

	// Close signals the end of the stream.
	Close() error
}

// OperatorContext provides operators with access to system services.
type OperatorContext interface {
	context.Context

	// OperatorID returns the ID of the current operator.
	OperatorID() string

	// GetState returns a state handle for the operator.
	GetState(name string) StateStore

	// SetTimer registers a callback for a specific timestamp.
	SetTimer(timestamp time.Time, callback func(time.Time))
}

// StateStore provides access to persistent state for operators.
type StateStore interface {
	Get(key []byte) ([]byte, error)
	Put(key, value []byte) error
	Delete(key []byte) error
}
