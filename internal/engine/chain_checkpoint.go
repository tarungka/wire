package engine

import "context"

type checkpointIdentity struct{ id, epoch uint64 }

type chainCheckpointState struct {
	uploader   *checkpointUploader
	taskID     string
	pending    map[checkpointIdentity]bool
	endPending bool
	notify     func(context.Context, checkpointUploadResult) error
}

func (c *chainCheckpointState) submit(ctx context.Context, id, epoch uint64, data [][]byte) error {
	key := checkpointIdentity{id, epoch}
	if c.pending[key] {
		return nil
	}
	c.pending[key] = true
	err := c.uploader.Submit(TaskCheckpoint{TaskID: c.taskID, CheckpointID: id, EpochID: epoch, Operators: data})
	if err != nil {
		return c.notify(ctx, checkpointUploadResult{CheckpointID: id, EpochID: epoch, Err: err})
	}
	c.pending[key] = true
	return nil
}
func (c *chainCheckpointState) complete(ctx context.Context, result checkpointUploadResult) error {
	<-c.uploader.slots
	key := checkpointIdentity{result.CheckpointID, result.EpochID}
	if !c.pending[key] {
		return nil
	}
	if result.Err == nil {
		delete(c.pending, key)
	}
	return c.notify(ctx, result)
}
func (c *chainCheckpointState) abort(id, epoch uint64) {
	delete(c.pending, checkpointIdentity{id, epoch})
	c.uploader.Cancel(id, epoch)
}
