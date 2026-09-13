package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckpointGoroutineAccounting(t *testing.T) {
	var count atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, taskGoroutineKey{}, &count)
	started := make(chan struct{})
	uploader, err := newCheckpointUploader(ctx, 1, checkpointReplicatorFunc(func(ctx context.Context, _ TaskCheckpoint) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer uploader.Close()
	if count.Load() != 0 {
		t.Fatal("idle uploader counted")
	}
	if err := uploader.Submit(TaskCheckpoint{CheckpointID: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("upload did not start")
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("active count=%d", got)
	}
	uploader.Close()
	if got := count.Load(); got != 0 {
		t.Fatalf("joined count=%d", got)
	}
}
