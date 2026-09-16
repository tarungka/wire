package worker

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
)

func TestAbsentCancellationDoesNotBlockCommands(t *testing.T) {
	a, b := net.Pipe()
	caller, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := yamux.Server(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(rpc.DefaultConfig())
	entered := make(chan struct{}, 32)
	server.Register(rpc.MethodUpdateTaskStatus, func(ctx context.Context, _ uint64, _ []byte) (any, *rpc.RPCError) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, rpc.NewRPCError(rpc.ErrCodeTimeout, ctx.Err().Error())
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, peer) }()
	defer func() { cancel(); _ = caller.Close(); server.Stop(); <-done }()
	w := New(Config{WorkerID: "worker"}, zerolog.Nop())
	w.epoch = 5
	w.client = rpc.NewClient(caller, rpc.DefaultConfig())
	taskCtx, taskCancel := context.WithCancel(context.Background())
	defer taskCancel()
	w.tasks["live"] = &taskHandle{jobID: "job", epoch: 5, attemptID: "live", cancel: taskCancel}
	absent := rpc.WorkerCommand{Type: rpc.CommandTypeCancelTask, JobID: "job", TaskID: "absent", EpochID: 5, AttemptID: "old"}
	dispatched := make(chan struct{})
	go func() { w.handleCommands([]rpc.WorkerCommand{absent}); close(dispatched) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("ack not sent")
	}
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("absent cancellation blocked command loop")
	}
	// Repeated cancels coalesce while the first status RPC is blocked.
	for range 100 {
		w.handleCommands([]rpc.WorkerCommand{absent})
	}
	w.handleCommands([]rpc.WorkerCommand{{Type: rpc.CommandTypeCancelTask, JobID: "job", TaskID: "live", EpochID: 5, AttemptID: "live"}})
	select {
	case <-taskCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("later cancel blocked")
	}
	w.mu.RLock()
	pending := len(w.cancelAcks)
	w.mu.RUnlock()
	if pending != 1 {
		t.Fatalf("pending acknowledgements=%d", pending)
	}
	select {
	case <-entered:
		t.Fatal("duplicate in-flight acknowledgement")
	default:
	}
}
