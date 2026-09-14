package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestPausedSenderWakesOnDownstreamHalfClose(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	out, err := client.Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	in, err := server.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out.setPaused(true)
	result := make(chan error, 1)
	go func() { result <- out.WriteMessage(&protocol.DataRecordMsg{Value: []byte("blocked")}) }()
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("paused sender ignored downstream EOF")
	}
	if out.session.IsClosed() {
		t.Fatal("half-close killed shared session")
	}
}

func TestSlowReceiverWindowResumesAfterConnectionTimeout(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	out, err := client.Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	in, err := server.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out.cfg.ConnectionWriteTimeout = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- out.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: make([]byte, 2*DefaultMaxStreamWindowSize)})
	}()
	select {
	case err := <-result:
		t.Fatalf("window exhaustion ended write: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	msg, err := in.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.(*protocol.DataRecordMsg).Value) != 2*DefaultMaxStreamWindowSize {
		t.Fatal("record truncated")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestCancelledBacklogOpenPreservesSession(t *testing.T) {
	client, server := negotiationPair(t)
	a, b := DefaultConfig(), DefaultConfig()
	a.NodeID, b.NodeID = "client", "server"
	result := make(chan error, 1)
	go func() { _, err := server.NegotiateSession(context.Background(), b, false); result <- err }()
	if _, err := client.NegotiateSession(context.Background(), a, true); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	live, err := client.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := server.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	// Fill the unacknowledged SYN semaphore without accepting new streams.
	for i := 0; i < a.yamuxConfig().AcceptBacklog; i++ {
		if _, err := client.OpenStream(); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := client.OpenDataStream(ctx, a, protocol.StreamHeaderMsg{SourceTaskID: "a", TargetTaskID: "b"}); !errors.Is(err, protocol.ErrHandshakeTimeout) {
		t.Fatalf("open: %v", err)
	}
	if client.IsClosed() {
		t.Fatal("cancelled open closed shared session")
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() { _, err := live.Write([]byte{42}); result <- err }()
	var data [1]byte
	if _, err := io.ReadFull(peer, data[:]); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if data[0] != 42 {
		t.Fatal("live stream changed")
	}
}

func TestWindowBlockedSenderWakesOnDownstreamHalfClose(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%t", secure), func(t *testing.T) {
			server, client, addr := newTestMuxPairSecure(t, secure)
			out, err := client.Dial(context.Background(), addr)
			if err != nil {
				t.Fatal(err)
			}
			in, err := server.Accept(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				result <- out.WriteMessage(&protocol.DataRecordMsg{Value: make([]byte, 2*DefaultMaxStreamWindowSize)})
			}()
			// Consume only the prefix to prove the write started, leaving the
			// 2 MiB frame larger than the receiver's available window.
			if err := in.raw.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			var prefix [protocol.LengthFieldSize]byte
			if _, err := io.ReadFull(in.raw, prefix[:]); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				t.Fatalf("write finished before receiver close: %v", err)
			default:
			}
			if err := in.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("partial frame succeeded after receiver close")
				}
			case <-time.After(time.Second):
				t.Fatal("window-blocked write ignored downstream EOF")
			}
			if out.session.IsClosed() {
				t.Fatal("stream failure closed shared session")
			}
		})
	}
}

func TestRestoringTaskInputsDoNotBlockOtherTasks(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.RegisterTaskInputs("restoring", 128); err != nil {
		t.Fatal(err)
	}
	defer server.UnregisterTask("restoring")
	if err := server.RegisterTaskInputs("ready", 1); err != nil {
		t.Fatal(err)
	}
	defer server.UnregisterTask("ready")
	// Include an excess input: even overflow must not hold the peer accept loop.
	for i := range 129 {
		stream, err := client.Dial(ctx, addr, protocol.StreamHeaderMsg{SourceTaskID: fmt.Sprintf("source-%d", i), TargetTaskID: "restoring"})
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
	}
	stream, err := client.Dial(ctx, addr, protocol.StreamHeaderMsg{SourceTaskID: "live", TargetTaskID: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	input, err := server.AcceptTask(ctx, "ready")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	for range 128 {
		input, err := server.AcceptTask(ctx, "restoring")
		if err != nil {
			t.Fatal(err)
		}
		_ = input.Close()
	}
}
