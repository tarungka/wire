package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestTaskFailureRedactsStatusStackAndObserver(t *testing.T) {
	a, b := net.Pipe()
	caller, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := yamux.Server(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	defer receiver.Close()
	server := rpc.NewServer(rpc.DefaultConfig())
	statuses := make(chan rpc.UpdateTaskStatusRequest, 2)
	server.Register(rpc.MethodUpdateTaskStatus, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var req rpc.UpdateTaskStatusRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "bad status")
		}
		statuses <- req
		return &rpc.UpdateTaskStatusResponse{}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, receiver) }()
	defer func() { cancel(); server.Stop(); <-done }()
	var observed error
	w := New(Config{WorkerID: "worker", TaskFailureObserver: func(_, _ string, err error) { observed = err }}, zerolog.Nop())
	w.client = rpc.NewClient(caller, rpc.DefaultConfig())
	_, taskCancel := context.WithCancel(t.Context())
	defer taskCancel()
	w.installTaskLocked("job", "task", rpc.TaskDescriptor{SecretValues: []string{"private-token"}}, taskCancel)
	for _, failure := range []error{&engine.OperatorPanicError{Value: "private-token", Stack: "stack private-token"}, fmt.Errorf("private-token: %w", errCheckpointUnavailable)} {
		w.reportTaskFailed("job", "task", failure)
		status := <-statuses
		if status.Failure == nil || strings.Contains(status.Failure.ErrorMessage, "private-token") || strings.Contains(status.Failure.StackTrace, "private-token") || strings.Contains(observed.Error(), "private-token") {
			t.Fatal("task diagnostic leaked credential")
		}
		if errors.Is(failure, errCheckpointUnavailable) && (status.Failure.ErrorClass != "checkpoint_unavailable" || !errors.Is(observed, errCheckpointUnavailable)) {
			t.Fatal("redaction changed recovery classification")
		}
		if errors.Is(failure, engine.ErrOperatorPanic) && status.Failure.StackTrace != "stack [REDACTED]" {
			t.Fatal("lost sanitized panic stack")
		}
	}
}
