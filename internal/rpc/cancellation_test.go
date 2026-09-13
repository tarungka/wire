package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
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

func TestCancelledOpenBoundsPendingWork(t *testing.T) {
	session, peer := testYamuxPair(t)
	var first *yamux.Stream
	for i := 0; i < yamux.DefaultConfig().AcceptBacklog; i++ {
		stream, err := session.OpenStream()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = stream
		}
	}
	client := NewClient(session, DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if stream, err := client.openStreamContext(ctx); !errors.Is(err, context.DeadlineExceeded) || stream != nil {
		t.Fatalf("blocked open: %v %v", stream, err)
	}
	if len(client.openGate) != 1 {
		t.Fatal("pending open not retained in bound")
	}
	// Further callers must wait for that same slot, not start more opens.
	for i := 0; i < 10; i++ {
		queued, stop := context.WithTimeout(context.Background(), time.Millisecond)
		_, err := client.openStreamContext(queued)
		stop()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued open: %v", err)
		}
	}
	accepted, err := peer.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = accepted.Close() }()
	deadline := time.Now().Add(time.Second)
	for len(client.openGate) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("late stream open did not release slot")
		}
		time.Sleep(time.Millisecond)
	}
	if session.IsClosed() {
		t.Fatal("cancelled open closed session")
	}
	if err := accepted.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte{42}); err != nil {
		t.Fatal(err)
	}
	var value [1]byte
	if _, err := accepted.Read(value[:]); err != nil || value[0] != 42 {
		t.Fatalf("live sibling: %v %v", value, err)
	}
}
