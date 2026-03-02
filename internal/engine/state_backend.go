package engine

import "errors"

// StateBackendType identifies a state backend implementation.
type StateBackendType string

const (
	// StateBackendPebble selects the PebbleDB-backed state backend (default).
	StateBackendPebble StateBackendType = "pebble"
	// StateBackendHashMap selects the in-memory B-tree state backend.
	StateBackendHashMap StateBackendType = "hashmap"
)

var (
	// ErrKeyNotFound indicates the requested key does not exist in the state backend.
	ErrKeyNotFound = errors.New("engine: key not found")

	// ErrBackendClosed indicates the state backend has been closed.
	ErrBackendClosed = errors.New("engine: state backend closed")

	// ErrMemoryLimitExceeded indicates a Put would exceed the configured memory limit.
	ErrMemoryLimitExceeded = errors.New("engine: state backend memory limit exceeded")

	// ErrSnapshotCorrupt indicates a snapshot failed integrity verification.
	ErrSnapshotCorrupt = errors.New("engine: snapshot corrupt")

	// ErrUnknownBackendType indicates an unrecognized state backend type string.
	ErrUnknownBackendType = errors.New("engine: unknown state backend type")
)

// StateBackend is the storage interface for operator state. Implementations
// must be safe for concurrent use by a single task's goroutines (operator
// chain + checkpoint). Cross-task isolation is guaranteed by giving each
// task its own StateBackend instance.
type StateBackend interface {
	// Put stores a key-value pair. Returns ErrMemoryLimitExceeded if the
	// backend has a memory limit and the write would exceed it.
	Put(key, value []byte) error

	// Get retrieves the value for key. Returns ErrKeyNotFound if the key
	// does not exist. The returned slice is owned by the caller.
	Get(key []byte) ([]byte, error)

	// Delete removes a key. Returns ErrKeyNotFound if the key does not exist.
	Delete(key []byte) error

	// NewIterator returns an iterator over all keys with the given prefix.
	// The caller must call Close on the returned iterator when done.
	NewIterator(prefix []byte) StateIterator

	// Checkpoint creates a consistent snapshot of the current state.
	// The returned SnapshotHandle can be passed to Restore to recover
	// the state. The handle includes an integrity checksum.
	Checkpoint(checkpointID uint64) (SnapshotHandle, error)

	// Restore replaces the backend state with the contents of the snapshot.
	// Returns ErrSnapshotCorrupt if integrity verification fails.
	Restore(handle SnapshotHandle) error

	// Close releases all resources. After Close, all other methods return
	// ErrBackendClosed.
	Close() error
}

// StateIterator iterates over key-value pairs in prefix order.
type StateIterator interface {
	// Next advances the iterator and returns true if there is a next entry.
	// Must be called before the first Key/Value access.
	Next() bool

	// Key returns the key at the current position. The returned slice is
	// valid only until the next call to Next or Close.
	Key() []byte

	// Value returns the value at the current position. The returned slice
	// is valid only until the next call to Next or Close.
	Value() []byte

	// Close releases iterator resources.
	Close()
}

// SnapshotHandle is an opaque reference to a checkpoint snapshot.
// For in-memory backends this contains the serialized state bytes.
// For disk-based backends this may contain a path or manifest reference.
type SnapshotHandle struct {
	// CheckpointID is the checkpoint that produced this snapshot.
	CheckpointID uint64

	// BackendType identifies which backend created the snapshot.
	BackendType StateBackendType

	// Data holds the serialized snapshot (in-memory backends) or a
	// serialized manifest (disk backends).
	Data []byte
}

// StateBackendMetrics abstracts state backend metrics collection.
type StateBackendMetrics interface {
	// ObserveStateSize records the current state size in bytes.
	ObserveStateSize(backendType string, sizeBytes int64)
	// IncCheckpointTotal increments the checkpoint count.
	IncCheckpointTotal(backendType string)
	// IncRestoreTotal increments the restore count.
	IncRestoreTotal(backendType string)
}

// noopStateBackendMetrics is a no-op implementation.
type noopStateBackendMetrics struct{}

func (noopStateBackendMetrics) ObserveStateSize(_ string, _ int64) {}
func (noopStateBackendMetrics) IncCheckpointTotal(_ string)        {}
func (noopStateBackendMetrics) IncRestoreTotal(_ string)           {}

// NoopStateBackendMetrics returns a StateBackendMetrics that discards all observations.
func NoopStateBackendMetrics() StateBackendMetrics {
	return noopStateBackendMetrics{}
}
