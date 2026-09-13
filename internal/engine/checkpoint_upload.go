package engine

import (
	"context"
	"errors"
	"io"
	"sync"
)

// TaskCheckpoint owns immutable operator snapshot bytes for one aligned epoch.
// Operator positions correspond to the task's ordered operator chain.
type TaskCheckpoint struct {
	TaskID       string
	CheckpointID uint64
	EpochID      uint64
	Operators    [][]byte
}

// CheckpointReplicator durably replicates an immutable checkpoint. Returning nil
// means the configured durability requirement has been met, not merely queued.
// Implementations must honor cancellation and must not mutate checkpoint bytes.
type CheckpointReplicator interface {
	Replicate(context.Context, TaskCheckpoint) error
}

var errCheckpointUploadBusy = errors.New("checkpoint upload capacity exhausted")
var errCheckpointUploaderClosed = errors.New("checkpoint uploader closed")

type checkpointUploadResult struct {
	CheckpointID uint64
	EpochID      uint64
	Err          error
}

// checkpointUploader bounds running uploads AND unconsumed completions. There
// are no idle worker goroutines or unbounded pending snapshot queues. Submit
// never waits for network I/O; the chain consumes results before admitting more.
type checkpointUploader struct {
	mu         sync.Mutex
	closed     bool
	closeOnce  sync.Once
	ctx        context.Context
	cancel     context.CancelFunc
	replicator CheckpointReplicator
	slots      chan struct{}
	results    chan checkpointUploadResult
	wg         sync.WaitGroup
}

func newCheckpointUploader(ctx context.Context, concurrency int, replicator CheckpointReplicator) (*checkpointUploader, error) {
	if concurrency < 1 || replicator == nil {
		return nil, errors.New("checkpoint uploader requires positive concurrency and a replicator")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &checkpointUploader{ctx: ctx, cancel: cancel, replicator: replicator, slots: make(chan struct{}, concurrency), results: make(chan checkpointUploadResult, concurrency)}, nil
}

func (u *checkpointUploader) Submit(snapshot TaskCheckpoint) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return errCheckpointUploaderClosed
	}
	if err := u.ctx.Err(); err != nil {
		return err
	}
	if snapshot.CheckpointID == 0 {
		return errors.New("checkpoint ID must be nonzero")
	}
	select {
	case u.slots <- struct{}{}:
	default:
		return errCheckpointUploadBusy
	}
	// Operators may reuse their snapshot buffers after Checkpoint returns. Copy
	// synchronously before returning control to processing/user code.
	owned := snapshot
	owned.Operators = make([][]byte, len(snapshot.Operators))
	for i, data := range snapshot.Operators {
		owned.Operators[i] = append([]byte(nil), data...)
	}
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		err := invokeOperator(func() error { return u.replicator.Replicate(u.ctx, owned) })
		result := checkpointUploadResult{CheckpointID: owned.CheckpointID, EpochID: owned.EpochID, Err: err}
		select {
		case u.results <- result:
		case <-u.ctx.Done():
			<-u.slots
		}
	}()
	return nil
}

func (u *checkpointUploader) Receive(ctx context.Context) (checkpointUploadResult, error) {
	select {
	case result, ok := <-u.results:
		if !ok {
			return checkpointUploadResult{}, io.EOF
		}
		<-u.slots
		return result, nil
	case <-ctx.Done():
		return checkpointUploadResult{}, ctx.Err()
	}
}

func (u *checkpointUploader) Close() {
	u.closeOnce.Do(func() {
		u.mu.Lock()
		u.closed = true
		u.mu.Unlock()
		u.cancel()
		u.wg.Wait()
		close(u.results)
	})
}
