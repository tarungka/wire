package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

type checkpointReplicatorFunc func(context.Context, TaskCheckpoint) error

func (f checkpointReplicatorFunc) Replicate(ctx context.Context, s TaskCheckpoint) error {
	return f(ctx, s)
}

func TestCheckpointUploadIsBoundedAndOwnsSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	received := make(chan TaskCheckpoint, 1)
	u, err := newCheckpointUploader(ctx, 1, checkpointReplicatorFunc(func(ctx context.Context, s TaskCheckpoint) error {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		received <- s
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	snapshot := TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, Operators: [][]byte{[]byte("before")}}
	if err := u.Submit(snapshot); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("upload did not start")
	}
	snapshot.Operators[0][0] = 'X'
	if err := u.Submit(TaskCheckpoint{CheckpointID: 8}); !errors.Is(err, errCheckpointUploadBusy) {
		t.Fatalf("unbounded admission: %v", err)
	}
	close(release)
	select {
	case s := <-received:
		if string(s.Operators[0]) != "before" {
			t.Fatal("processing mutated in-flight checkpoint")
		}
	case <-ctx.Done():
		t.Fatal("upload did not finish")
	}
	// Even after I/O finishes, capacity includes the undelivered completion.
	if err := u.Submit(TaskCheckpoint{CheckpointID: 8}); !errors.Is(err, errCheckpointUploadBusy) {
		t.Fatalf("completion queue not bounded: %v", err)
	}
	result, err := u.Receive(ctx)
	if err != nil || result.Err != nil || result.CheckpointID != 7 || result.EpochID != 2 {
		t.Fatalf("completion: %+v %v", result, err)
	}
}

func TestCheckpointUploaderCloseCancelsAndJoins(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	u, err := newCheckpointUploader(context.Background(), 1, checkpointReplicatorFunc(func(ctx context.Context, _ TaskCheckpoint) error {
		close(entered)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Submit(TaskCheckpoint{CheckpointID: 1}); err != nil {
		t.Fatal(err)
	}
	<-entered
	u.Close()
	select {
	case <-exited:
	default:
		t.Fatal("uploader returned before worker exit")
	}
	if err := u.Submit(TaskCheckpoint{CheckpointID: 2}); !errors.Is(err, errCheckpointUploaderClosed) {
		t.Fatal(err)
	}
	u.Close()
}

func TestCheckpointUploadFailureIsACompletion(t *testing.T) {
	for _, panics := range []bool{false, true} {
		sentinel := errors.New("replica unavailable")
		u, err := newCheckpointUploader(context.Background(), 1, checkpointReplicatorFunc(func(context.Context, TaskCheckpoint) error {
			if panics {
				panic("replica panic")
			}
			return sentinel
		}))
		if err != nil {
			t.Fatal(err)
		}
		if err := u.Submit(TaskCheckpoint{CheckpointID: 3}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, err := u.Receive(ctx)
		cancel()
		u.Close()
		want := sentinel
		if panics {
			want = ErrOperatorPanic
		}
		if err != nil || !errors.Is(result.Err, want) {
			t.Fatalf("failure completion: %+v %v", result, err)
		}
	}
}
