package transport

// SetCheckpointCompletionReader connects the stream to its job's authoritative
// globally completed checkpoint. The reader must be safe for concurrent calls.
// Receipt or alignment of a barrier alone must never advance this watermark.
func (fs *FrameStream) SetCheckpointCompletionReader(reader func() uint64) {
	fs.mu.Lock()
	fs.checkpointCompletion = reader
	fs.mu.Unlock()
}

// MarkCheckpointCompleted records a completion notification or recovery point.
// Stale notifications cannot lower the watermark.
func (fs *FrameStream) MarkCheckpointCompleted(checkpointID uint64) {
	fs.mu.Lock()
	fs.lastCompletedCheckpoint = max(fs.lastCompletedCheckpoint, checkpointID)
	fs.mu.Unlock()
}

// IsCheckpointCompleted can also be checked after a buffered read: completion
// may occur while a barrier waits in an application queue.
func (fs *FrameStream) IsCheckpointCompleted(checkpointID uint64) bool {
	fs.mu.Lock()
	completed, reader := fs.lastCompletedCheckpoint, fs.checkpointCompletion
	fs.mu.Unlock()
	if reader != nil {
		completed = max(completed, reader())
	}
	return checkpointID != 0 && checkpointID <= completed
}
