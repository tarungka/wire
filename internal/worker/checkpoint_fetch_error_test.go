package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCheckpointFetchErrorPreservesTransientFailures(t *testing.T) {
	for _, tc := range []struct {
		err       error
		permanent bool
	}{
		{os.ErrNotExist, true}, {engine.ErrCheckpointFileCorrupt, true},
		{context.Canceled, false}, {context.DeadlineExceeded, false},
		{os.ErrPermission, false}, {errors.New("temporary I/O failure"), false},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			got := checkpointFetchError(fmt.Errorf("read checkpoint: %w", tc.err))
			var remote *rpc.RPCError
			permanent := errors.As(got, &remote) && remote.Code == rpc.ErrCodeUnknownCheckpoint
			if permanent != tc.permanent {
				t.Fatalf("incorrect classification: %v", got)
			}
			if !tc.permanent && !errors.Is(got, tc.err) {
				t.Fatal("lost retryable cause")
			}
		})
	}
}

func TestCancelledArchiveLoadDoesNotInvalidateCheckpoint(t *testing.T) {
	store, err := engine.NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	loader := checkpointArchiveLoader(store, t.TempDir(), func(context.Context, rpc.FetchCheckpointRequest) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = loader(ctx, rpc.FetchCheckpointRequest{JobID: "job", TaskID: "task", CheckpointID: 1, EpochID: 1, RequireArchive: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel invalidated checkpoint: %v", err)
	}
}
