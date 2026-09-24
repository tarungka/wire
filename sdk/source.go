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
	// Deprecated: runtime watermarks use SetWatermarkStrategy, defaulting to
	// bounded out-of-orderness with a five-second tolerance. This method remains
	// in the interface for source compatibility and is not called by execution.
	GenerateWatermark() int64
}

// CheckpointedSource optionally exposes source offsets to checkpoint adapters.
// RestoreOffset does not itself guarantee replay: the connector documents its
// external replay requirements and must reject unsupported offsets.
type CheckpointedSource interface {
	Source
	Checkpoint(checkpointID uint64) ([]byte, error)
	RestoreOffset(ctx context.Context, offset []byte) error
}

// PreOpenCheckpointedSource opts into restoration before Open. The method must
// only load offsets; resource acquisition belongs in Open. The runtime calls this
// instead of RestoreOffset on recovery, before exposing an ingress listener.
type PreOpenCheckpointedSource interface {
	CheckpointedSource
	RestoreOffsetBeforeOpen(context.Context, []byte) error
}
