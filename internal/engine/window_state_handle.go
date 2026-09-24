package engine

import (
	"os"
)

// CheckpointState exposes the selected backend to archive replication and typed
// restore. Checkpoint remains available for legacy portable processor snapshots.
func (op *EventTimeWindowOperator) CheckpointState(id uint64) (SnapshotHandle, error) {
	op.processor.mu.Lock()
	defer op.processor.mu.Unlock()
	if op.backend == nil {
		return SnapshotHandle{}, ErrBackendClosed
	}
	return op.backend.Checkpoint(id)
}

// RestoreState validates both the backend snapshot and window records before
// changing the live generation. Legacy task snapshots use RestoreCheckpoint.
func (op *EventTimeWindowOperator) RestoreState(handle SnapshotHandle) error {
	op.processor.mu.Lock()
	defer op.processor.mu.Unlock()
	if op.backend == nil {
		return ErrBackendClosed
	}
	root, err := os.MkdirTemp("", "wire-window-restore-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	source, err := NewStateBackend(StateBackendConfig{Type: handle.BackendType, PebbleDataDir: root})
	if err != nil {
		return err
	}
	defer source.Close()
	if err := source.Restore(handle); err != nil {
		return err
	}
	candidate, err := NewWindowProcessor(op.processor.config, op.processor.aggregator)
	if err != nil {
		return err
	}
	candidate.numKeyGroups = op.processor.numKeyGroups
	if err := candidate.BindBackend(source); err != nil {
		return err
	}
	if err := op.backend.Restore(handle); err != nil {
		return err
	}
	op.processor.windows = candidate.windows
	op.processor.watermark = candidate.watermark
	op.processor.stats = candidate.stats
	op.processor.groupWatermarks = candidate.groupWatermarks
	return nil
}
