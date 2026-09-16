package worker

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

func TestHeartbeatPartitionStopsActiveWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := rpc.NewServer(rpc.DefaultConfig())
	var running atomic.Bool
	server.Register(rpc.MethodRegisterWorker, func(context.Context, uint64, []byte) (any, *rpc.RPCError) {
		return &rpc.RegisterWorkerResponse{Epoch: 1}, nil
	})
	server.Register(rpc.MethodHeartbeat, func(ctx context.Context, _ uint64, _ []byte) (any, *rpc.RPCError) {
		<-ctx.Done()
		return nil, rpc.NewRPCError(rpc.ErrCodeTimeout, "partition")
	})
	server.Register(rpc.MethodUpdateTaskStatus, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var req rpc.UpdateTaskStatusRequest
		if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &req); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, err.Error())
		}
		if req.Status == rpc.TaskStatusRunning {
			running.Store(true)
		}
		return &rpc.UpdateTaskStatusResponse{Accepted: true}, nil
	})
	server.RegisterStream(rpc.MethodWatchCommands, func(ctx context.Context, _ uint64, _ []byte, _ *yamux.Stream) error { <-ctx.Done(); return nil })
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		session, err := transport.NewServerSession(conn, transport.DefaultConfig())
		if err != nil {
			return
		}
		defer session.Close()
		server.ServeSession(ctx, session.YamuxSession())
	}()
	defer func() { cancel(); _ = listener.Close(); server.Stop(); <-serverDone }()
	source := &lifecycleSource{block: true, running: &running}
	sink := &lifecycleSink{}
	registry, desc := lifecyclePipeline(source, &lifecycleMap{}, sink)
	w := NewWithRegistry(Config{WorkerID: "worker", CoordinatorAddr: listener.Addr().String(), TaskSlots: 1, HeartbeatInterval: 20 * time.Millisecond, HeartbeatTimeout: 300 * time.Millisecond}, registry, zerolog.Nop())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	for {
		w.mu.RLock()
		epoch := w.epoch
		w.mu.RUnlock()
		if epoch == 1 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("worker stopped before registration: %v", err)
		case <-ctx.Done():
			t.Fatal("registration timed out")
		case <-time.After(time.Millisecond):
		}
	}
	desc.TaskID = "task"
	desc.AttemptID = "attempt"
	desc.EpochID = 1
	if _, err := w.handleRequestTaskSlots(ctx, 1, reservationPayload(t, rpc.RequestTaskSlotsRequest{JobID: "job", EpochID: 1, ReservationID: "attempt", RequiredSlots: 1})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.handleSubmitJob(ctx, 2, reservationPayload(t, rpc.SubmitJobRequest{JobID: "job", EpochID: 1, AttemptID: "attempt", ReservationID: "attempt", Tasks: []rpc.TaskDescriptor{desc}})); err != nil {
		t.Fatal(err)
	}
	for !running.Load() {
		select {
		case <-ctx.Done():
			t.Fatal("task did not start")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrCoordinatorContactLost) {
			t.Fatalf("partition exit: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not self-terminate")
	}
	if source.closed.Load() != 1 || sink.closed.Load() != 1 {
		t.Fatal("active operators not closed")
	}
	w.mu.RLock()
	session := w.session
	address := w.data.ListenAddr()
	pending := len(w.tasks)
	w.mu.RUnlock()
	if pending != 0 || !session.YamuxSession().IsClosed() {
		t.Fatal("task or RPC survived contact loss")
	}
	conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("data listener survived contact loss")
	}
}
