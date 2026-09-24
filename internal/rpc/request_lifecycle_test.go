package rpc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUnaryHandlerCancelledWhenCallerLeaves(t *testing.T) {
	caller, peer := testYamuxPair(t)
	server := NewServer(DefaultConfig())
	entered, exited := make(chan struct{}), make(chan struct{})
	server.Register(MethodHeartbeat, func(ctx context.Context, _ uint64, _ []byte) (any, *RPCError) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, NewRPCError(ErrCodeTimeout, "cancelled")
	})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go server.ServeSession(ctx, peer)
	callCtx, cancel := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		var result HeartbeatResponse
		finished <- NewClient(caller, DefaultConfig()).Call(callCtx, MethodHeartbeat, &HeartbeatRequest{}, &result)
	}()
	<-entered
	cancel()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("handler outlived caller")
	}
	<-finished
	stop()
	server.Stop()
}

func TestRPCRejectsMismatchedResponse(t *testing.T) {
	for _, wrongMethod := range []bool{false, true} {
		caller, peer := testYamuxPair(t)
		done := make(chan struct{})
		go func() {
			defer close(done)
			stream, err := peer.AcceptStream()
			if err != nil {
				return
			}
			defer stream.Close()
			frame, err := ReadRPCFrame(stream, MaxRPCPayloadSize)
			if err != nil {
				return
			}
			method, id := frame.MethodID, frame.RequestID
			if wrongMethod {
				method = MethodSubmitJob
			} else {
				id++
			}
			_ = EncodeRPCRequest(stream, method, id, &HeartbeatResponse{Accepted: true})
		}()
		var result HeartbeatResponse
		err := NewClient(caller, DefaultConfig()).Call(context.Background(), MethodHeartbeat, &HeartbeatRequest{}, &result)
		if !errors.Is(err, ErrRPCDecodeFailed) {
			t.Fatalf("accepted unrelated response: %v", err)
		}
		<-done
	}
}

func TestIdempotentRPCRetriesLostReply(t *testing.T) {
	caller, peer := testYamuxPair(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for attempt := 0; attempt < 2; attempt++ {
			stream, err := peer.AcceptStream()
			if err != nil {
				return
			}
			frame, err := ReadRPCFrame(stream, MaxRPCPayloadSize)
			if err != nil {
				_ = stream.Close()
				return
			}
			if attempt == 1 {
				_ = EncodeRPCRequest(stream, frame.MethodID, frame.RequestID, &UpdateTaskStatusResponse{Accepted: true})
			}
			_ = stream.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := NewClient(caller, DefaultConfig()).UpdateTaskStatus(ctx, &UpdateTaskStatusRequest{TaskID: "task", AttemptID: "attempt"})
	if err != nil || !result.Accepted {
		t.Fatalf("retry failed: %v %v", result, err)
	}
	<-done
}

func TestUnaryServerEnforcesMethodDeadline(t *testing.T) {
	caller, peer := testYamuxPair(t)
	cfg := DefaultConfig()
	cfg.HeartbeatTimeout = 20 * time.Millisecond
	server := NewServer(cfg)
	result := make(chan error, 1)
	server.Register(MethodHeartbeat, func(ctx context.Context, _ uint64, _ []byte) (any, *RPCError) {
		<-ctx.Done()
		result <- ctx.Err()
		return nil, NewRPCError(ErrCodeTimeout, "deadline")
	})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go server.ServeSession(ctx, peer)
	// The caller's longer budget cannot extend the server's own handler budget.
	clientCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _ = NewClient(caller, DefaultConfig()).Heartbeat(clientCtx, &HeartbeatRequest{})
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("handler error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server ignored method budget")
	}
	stop()
	server.Stop()
}
