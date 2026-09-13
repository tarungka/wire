package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func negotiationPair(t *testing.T) (*Session, *Session) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	servers := make(chan *Session, 1)
	failures := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			failures <- err
			return
		}
		session, err := NewServerSession(conn, DefaultConfig())
		if err != nil {
			failures <- err
			return
		}
		servers <- session
	}()
	client, err := NewClientSession(ln.Addr().String(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	select {
	case server := <-servers:
		t.Cleanup(func() { _ = server.Close() })
		return client, server
	case err := <-failures:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("server creation timeout")
	}
	return nil, nil
}

func TestSessionNegotiationVersionsAndFeatures(t *testing.T) {
	for _, tc := range []struct {
		name             string
		version, minimum uint16
		incompatible     bool
	}{
		{"same version", 1, 1, false}, {"rolling upgrade", 2, 1, false}, {"incompatible", 2, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := negotiationPair(t)
			a, b := DefaultConfig(), DefaultConfig()
			a.NodeID = "upstream"
			b.NodeID = "downstream"
			a.LocalProtocolVersion = tc.version
			a.LocalMinVersion = tc.minimum
			a.LocalFeatures = protocol.FeatureCRC32C | protocol.FeatureCompression
			b.LocalFeatures = protocol.FeatureCRC32C
			result := make(chan error, 1)
			go func() { _, err := server.NegotiateSession(context.Background(), b, false); result <- err }()
			params, err := client.NegotiateSession(context.Background(), a, true)
			peerErr := <-result
			if tc.incompatible {
				if !errors.Is(err, protocol.ErrVersionIncompatible) || !errors.Is(peerErr, protocol.ErrVersionIncompatible) {
					t.Fatalf("errors: %v / %v", err, peerErr)
				}
				if !client.IsClosed() || !server.IsClosed() {
					t.Fatal("incompatible sessions left open")
				}
				return
			}
			if err != nil || peerErr != nil {
				t.Fatalf("errors: %v / %v", err, peerErr)
			}
			if params.EffectiveVersion != 1 || params.Features != protocol.FeatureCRC32C {
				t.Fatalf("negotiated %+v", params)
			}
			got, node, ok := server.SessionParameters()
			if !ok || got != params || node != "upstream" {
				t.Fatalf("server state %+v %q %v", got, node, ok)
			}
			// Idempotent calls reuse the one control stream, without another exchange.
			again, err := client.NegotiateSession(context.Background(), a, true)
			if err != nil || again != params {
				t.Fatal("negotiation not reusable")
			}
			stream, err := client.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			if stream.StreamID() != 3 {
				t.Fatalf("control stream was not first: data stream=%d", stream.StreamID())
			}
			_ = stream.Close()
		})
	}
}

func TestSessionNegotiationTimeoutClosesSession(t *testing.T) {
	_, server := negotiationPair(t)
	cfg := DefaultConfig()
	cfg.NodeID = "server"
	cfg.HandshakeTimeout = 20 * time.Millisecond
	_, err := server.NegotiateSession(context.Background(), cfg, false)
	if !errors.Is(err, protocol.ErrHandshakeTimeout) || !server.IsClosed() {
		t.Fatalf("err=%v closed=%v", err, server.IsClosed())
	}
}

func TestSessionNegotiationRejectsDataBeforeHandshake(t *testing.T) {
	client, server := negotiationPair(t)
	cfg := DefaultConfig()
	cfg.NodeID = "server"
	result := make(chan error, 1)
	go func() { _, err := server.NegotiateSession(context.Background(), cfg, false); result <- err }()
	stream, err := client.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.EncodeAndWriteFrame(stream, &protocol.DataRecordMsg{Value: []byte("premature")}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, protocol.ErrHandshakeExpected) {
		t.Fatalf("error=%v", err)
	}
	if !server.IsClosed() {
		t.Fatal("protocol violation left session open")
	}
}

func TestSessionDataStreamRouting(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint("known=", known), func(t *testing.T) {
			client, server := negotiationPair(t)
			cfg := DefaultConfig()
			cfg.NodeID = "test-worker"
			errs := make(chan error, 1)
			go func() { _, err := server.NegotiateSession(context.Background(), cfg, false); errs <- err }()
			if _, err := client.NegotiateSession(context.Background(), cfg, true); err != nil {
				t.Fatal(err)
			}
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			header := protocol.StreamHeaderMsg{SourceTaskID: "map-1", TargetTaskID: "reduce-2", PartitionIndex: 3}
			received := make(chan *FrameStream, 1)
			go func() {
				stream, err := server.AcceptDataStream(cfg, func(h protocol.StreamHeaderMsg) bool { return known && h == header })
				received <- stream
				errs <- err
			}()
			out, err := client.OpenDataStream(context.Background(), cfg, header)
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			in, err := <-received, <-errs
			if !known {
				if err == nil || in != nil {
					t.Fatalf("unknown target accepted: %v", err)
				}
				msg, err := out.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				eop, ok := msg.(*protocol.EndOfPartitionMsg)
				if !ok || eop.Reason != protocol.EndReasonError {
					t.Fatalf("rejection: %#v", msg)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			if got, ok := in.Header(); !ok || got != header {
				t.Fatalf("routing: %+v %v", got, ok)
			}
			if err := out.WriteMessage(&protocol.DataRecordMsg{Value: []byte("routed")}); err != nil {
				t.Fatal(err)
			}
			msg, err := in.ReadMessage()
			if err != nil || string(msg.(*protocol.DataRecordMsg).Value) != "routed" {
				t.Fatalf("record: %v %v", msg, err)
			}
		})
	}
}

func TestDataStreamRequiresNegotiation(t *testing.T) {
	client, server := negotiationPair(t)
	cfg := DefaultConfig()
	if _, err := client.OpenDataStream(context.Background(), cfg, protocol.StreamHeaderMsg{SourceTaskID: "a", TargetTaskID: "b"}); err == nil {
		t.Fatal("opened before negotiation")
	}
	if _, err := server.AcceptDataStream(cfg, nil); err == nil {
		t.Fatal("accepted before negotiation")
	}
}

func TestDataStreamInvalidFirstFrame(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint("missing=", missing), func(t *testing.T) {
			client, server := negotiationPair(t)
			cfg := DefaultConfig()
			cfg.NodeID = "worker"
			cfg.HandshakeTimeout = 50 * time.Millisecond
			errs := make(chan error, 1)
			go func() { _, err := server.NegotiateSession(context.Background(), cfg, false); errs <- err }()
			if _, err := client.NegotiateSession(context.Background(), cfg, true); err != nil {
				t.Fatal(err)
			}
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			go func() {
				_, err := server.AcceptDataStream(cfg, func(protocol.StreamHeaderMsg) bool { return true })
				errs <- err
			}()
			raw, err := client.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if !missing {
				if err := protocol.EncodeAndWriteFrame(raw, &protocol.DataRecordMsg{Value: []byte("premature")}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-errs:
				want := protocol.ErrHandshakeExpected
				if missing {
					want = protocol.ErrHandshakeTimeout
				}
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("invalid stream stalled receiver")
			}
			if client.IsClosed() || server.IsClosed() {
				t.Fatal("bad data stream closed healthy session")
			}
		})
	}
}

func TestMuxRoutesNamedTasks(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	if err := server.RegisterTask("reduce-2"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	header := protocol.StreamHeaderMsg{SourceTaskID: "map-1", TargetTaskID: "reduce-2", PartitionIndex: 2}
	out, err := client.Dial(ctx, addr, header)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	in, err := server.AcceptTask(ctx, "reduce-2")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if got, ok := in.Header(); !ok || got != header {
		t.Fatalf("wrong task route: %+v", got)
	}
	if out.StreamID() != in.StreamID() {
		t.Fatal("inconsistent stream IDs")
	}
	if err := out.WriteMessage(&protocol.DataRecordMsg{Value: []byte("task-specific")}); err != nil {
		t.Fatal(err)
	}
	msg, err := in.ReadMessage()
	if err != nil || string(msg.(*protocol.DataRecordMsg).Value) != "task-specific" {
		t.Fatalf("delivery: %v %v", msg, err)
	}
	if len(server.streamCh) != 0 {
		t.Fatal("named task stream delivered to default queue")
	}
	unknown, err := client.Dial(ctx, addr, protocol.StreamHeaderMsg{SourceTaskID: "map-1", TargetTaskID: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	defer unknown.Close()
	rejected, err := unknown.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if eop, ok := rejected.(*protocol.EndOfPartitionMsg); !ok || eop.Reason != protocol.EndReasonError {
		t.Fatalf("unknown target not rejected: %v", rejected)
	}
}

func TestPartialDataFrameDeadline(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	in, err := server.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	in.cfg.FrameReadTimeout = 30 * time.Millisecond
	read := make(chan error, 1)
	go func() { _, err := in.ReadMessage(); read <- err }()
	// An idle data stream is valid even beyond its frame-completion timeout.
	select {
	case err := <-read:
		t.Fatalf("idle stream failed: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	if _, err := out.raw.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-read:
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("partial frame: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("partial frame did not time out")
	}
}

func TestPausedWriterCancellation(t *testing.T) {
	_, client, addr := newTestMuxPair(t)
	out, err := client.Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	out.setPaused(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := out.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: []byte("canceled")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("paused cancellation: %v", err)
	}
}

func TestDialWaiterCancellationDoesNotBlockOtherPeers(t *testing.T) {
	stalled, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	_, client, healthyAddr := newTestMuxPair(t)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { _, err := client.Dial(firstCtx, stalled.Addr().String()); first <- err }()
	// TCP acceptance proves the first dial owns the in-flight entry; deliberately
	// never create a Yamux server or respond to its session handshake.
	conn, err := stalled.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waiterCtx, cancelWaiter := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelWaiter()
	waiter := make(chan error, 1)
	go func() { _, err := client.Dial(waiterCtx, stalled.Addr().String()); waiter <- err }()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting dial: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dial waiter ignored its context")
	}
	healthyCtx, cancelHealthy := context.WithTimeout(context.Background(), time.Second)
	defer cancelHealthy()
	stream, err := client.Dial(healthyCtx, healthyAddr)
	if err != nil {
		t.Fatalf("stalled worker blocked another peer: %v", err)
	}
	defer stream.Close()
	cancelFirst()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("initiating dial: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("initiating dial did not cancel")
	}
}

func TestTaskUnregisterClosesPendingGeneration(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	const task = "restarting-task"
	if err := server.RegisterTask(task); err != nil {
		t.Fatal(err)
	}
	server.mu.RLock()
	previous := server.tasks[task]
	server.mu.RUnlock()
	old, err := client.Dial(context.Background(), addr, protocol.StreamHeaderMsg{SourceTaskID: "old-source", TargetTaskID: task})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	deadline := time.Now().Add(time.Second)
	for len(previous.streams) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("old stream was not queued")
		}
		time.Sleep(time.Millisecond)
	}
	server.UnregisterTask(task)
	if _, err := server.AcceptTask(context.Background(), task); err == nil {
		t.Fatal("unregistered task accepted a stream")
	}
	if err := server.RegisterTask(task); err != nil {
		t.Fatal(err)
	}
	current, err := client.Dial(context.Background(), addr, protocol.StreamHeaderMsg{SourceTaskID: "new-source", TargetTaskID: task})
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	input, err := server.AcceptTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	header, _ := input.Header()
	if header.SourceTaskID != "new-source" {
		t.Fatalf("old generation leaked: %+v", header)
	}
	if err := previous.enqueue(ctx, input); err == nil {
		t.Fatal("closed generation accepted late stream")
	}
}
