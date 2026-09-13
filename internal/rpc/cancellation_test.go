package rpc

import (
	"context"
	"testing"
	"time"
)

func TestCancelledCallPreservesSharedSession(t *testing.T) {
	clientSession, serverSession := testYamuxPair(t)
	cfg := DefaultConfig()
	server := NewServer(cfg)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	server.Register(MethodSubmitJob, func(context.Context, uint64, []byte) (any, *RPCError) {
		close(started)
		<-release
		return &SubmitJobResponse{}, nil
	})
	server.Register(MethodHeartbeat, func(context.Context, uint64, []byte) (any, *RPCError) { return &HeartbeatResponse{Accepted: true}, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go server.ServeSession(ctx, serverSession)
	client := NewClient(clientSession, cfg)
	callCtx, cancelCall := context.WithCancel(ctx)
	defer cancelCall()
	done := make(chan error, 1)
	go func() { done <- client.Call(callCtx, MethodSubmitJob, &SubmitJobRequest{}, &SubmitJobResponse{}) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("call did not start")
	}
	cancelCall()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled call succeeded")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("cancellation did not wake RPC read")
	}
	if clientSession.IsClosed() {
		t.Fatal("cancelled call closed shared session")
	}
	var response HeartbeatResponse
	if err := client.Call(ctx, MethodHeartbeat, &HeartbeatRequest{}, &response); err != nil || !response.Accepted {
		t.Fatalf("sibling call: %+v %v", response, err)
	}
}
