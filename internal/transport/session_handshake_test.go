package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func negotiationPair(t *testing.T, secure ...bool) (*Session, *Session) {
	t.Helper()
	cfg := DefaultConfig()
	if len(secure) > 0 && secure[0] {
		certs := generateTestCerts(t)
		var err error
		cfg.TLSConfig, err = LoadTLSConfig(certs.ServerCertFile, certs.ServerKeyFile, true, certs.CACertFile)
		if err != nil {
			t.Fatal(err)
		}
		cfg.TLSConfig.RootCAs = cfg.TLSConfig.ClientCAs
	}
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
		session, err := NewServerSession(conn, cfg)
		if err != nil {
			failures <- err
			return
		}
		servers <- session
	}()
	client, err := NewClientSession(ln.Addr().String(), cfg)
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
	t.Run("tcp", func(t *testing.T) { runTestSessionNegotiationVersionsAndFeatures(t, false) })
	t.Run("mutual_tls", func(t *testing.T) { runTestSessionNegotiationVersionsAndFeatures(t, true) })
}

func runTestSessionNegotiationVersionsAndFeatures(t *testing.T, secure bool) {
	for _, tc := range []struct {
		name             string
		version, minimum uint16
		incompatible     bool
	}{
		{"same version", 1, 1, false}, {"rolling upgrade", 2, 1, false}, {"incompatible", 2, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := negotiationPair(t, secure)
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
			header := protocol.StreamHeaderMsg{SourceTaskID: "source", TargetTaskID: "target"}
			stream, err := client.OpenDataStream(context.Background(), a, header)
			if err != nil {
				t.Fatal(err)
			}
			if stream.StreamID() != 3 {
				t.Fatalf("control stream was not first: data stream=%d", stream.StreamID())
			}
			defer stream.Close()
			input, err := server.AcceptDataStream(b, func(got protocol.StreamHeaderMsg) bool { return got == header })
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			inherited, err := input.ReceiveHandshake()
			if err != nil || *inherited != params {
				t.Fatalf("stream parameters: %v %v", inherited, err)
			}
			if err := stream.WriteMessage(&protocol.DataRecordMsg{Value: []byte("version-one-record")}); err != nil {
				t.Fatal(err)
			}
			message, err := input.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			record, ok := message.(*protocol.DataRecordMsg)
			if !ok || string(record.Value) != "version-one-record" {
				t.Fatalf("record after negotiation: %#v", message)
			}
			if err := stream.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "source"}); err != nil {
				t.Fatal(err)
			}
			if _, err := input.ReadMessage(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionNegotiationTimeoutClosesSession(t *testing.T) {
	t.Run("tcp", func(t *testing.T) { runTestSessionNegotiationTimeoutClosesSession(t, false) })
	t.Run("mutual_tls", func(t *testing.T) { runTestSessionNegotiationTimeoutClosesSession(t, true) })
}

func runTestSessionNegotiationTimeoutClosesSession(t *testing.T, secure bool) {
	_, server := negotiationPair(t, secure)
	cfg := DefaultConfig()
	cfg.NodeID = "server"
	cfg.HandshakeTimeout = 20 * time.Millisecond
	_, err := server.NegotiateSession(context.Background(), cfg, false)
	if !errors.Is(err, protocol.ErrHandshakeTimeout) || !server.IsClosed() {
		t.Fatalf("err=%v closed=%v", err, server.IsClosed())
	}
}

func TestSessionNegotiationRejectsDataBeforeHandshake(t *testing.T) {
	t.Run("tcp", func(t *testing.T) { runTestSessionNegotiationRejectsDataBeforeHandshake(t, false) })
	t.Run("mutual_tls", func(t *testing.T) { runTestSessionNegotiationRejectsDataBeforeHandshake(t, true) })
}

func runTestSessionNegotiationRejectsDataBeforeHandshake(t *testing.T, secure bool) {
	client, server := negotiationPair(t, secure)
	cfg := DefaultConfig()
	cfg.NodeID = "server"
	result := make(chan error, 1)
	go func() { _, err := server.NegotiateSession(context.Background(), cfg, false); result <- err }()
	stream, err := client.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	// The server closes the session after reading the invalid frame. That
	// closure can reach the client before its Write returns, so successful
	// delivery is established by the server's precise rejection below.
	writeErr := protocol.EncodeAndWriteFrame(stream, &protocol.DataRecordMsg{Value: []byte("premature")})
	select {
	case err := <-result:
		if !errors.Is(err, protocol.ErrHandshakeExpected) {
			t.Fatalf("error=%v (client write: %v)", err, writeErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("server did not reject premature data (client write: %v)", writeErr)
	}
	if !server.IsClosed() {
		t.Fatal("protocol violation left session open")
	}
}

func TestSessionDataStreamRouting(t *testing.T) {
	t.Run("tcp", func(t *testing.T) { runTestSessionDataStreamRouting(t, false) })
	t.Run("mutual_tls", func(t *testing.T) { runTestSessionDataStreamRouting(t, true) })
}

func runTestSessionDataStreamRouting(t *testing.T, secure bool) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint("known=", known), func(t *testing.T) {
			client, server := negotiationPair(t, secure)
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
	t.Run("tcp", func(t *testing.T) { runTestDataStreamRequiresNegotiation(t, false) })
	t.Run("mutual_tls", func(t *testing.T) { runTestDataStreamRequiresNegotiation(t, true) })
}

func runTestDataStreamRequiresNegotiation(t *testing.T, secure bool) {
	client, server := negotiationPair(t, secure)
	cfg := DefaultConfig()
	if _, err := client.OpenDataStream(context.Background(), cfg, protocol.StreamHeaderMsg{SourceTaskID: "a", TargetTaskID: "b"}); err == nil {
		t.Fatal("opened before negotiation")
	}
	if _, err := server.AcceptDataStream(cfg, nil); err == nil {
		t.Fatal("accepted before negotiation")
	}
}

func TestDataStreamInvalidFirstFrame(t *testing.T) {
	t.Run("tcp", func(t *testing.T) { runTestDataStreamInvalidFirstFrame(t, false) })
	t.Run("mutual_tls", func(t *testing.T) { runTestDataStreamInvalidFirstFrame(t, true) })
}

func runTestDataStreamInvalidFirstFrame(t *testing.T, secure bool) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint("missing=", missing), func(t *testing.T) {
			client, server := negotiationPair(t, secure)
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
	t.Run("tcp", func(t *testing.T) { runTestPartialDataFrameDeadline(t, false) })
	t.Run("mutual_tls", func(t *testing.T) { runTestPartialDataFrameDeadline(t, true) })
}

func runTestPartialDataFrameDeadline(t *testing.T, secure bool) {
	server, client, addr := newTestMuxPairSecure(t, secure)
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

func TestControlProgressAndCancellationWithExhaustedDataWindow(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx, cancel := context.WithCancel(context.Background())
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
	done := make(chan error, 1)
	go func() {
		done <- out.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: make([]byte, 2*DefaultMaxStreamWindowSize)})
	}()
	// Receiving the header establishes that the write started. Do not read its
	// body: it exceeds the receive window, so the writer cannot finish.
	var header [protocol.HeaderSize]byte
	if _, err := io.ReadFull(in.raw, header[:]); err != nil {
		t.Fatal(err)
	}
	// A second writer must be able to abandon the serialization queue
	// without interrupting the first caller's active frame.
	queuedCtx, cancelQueued := context.WithTimeout(context.Background(), 20*time.Millisecond)
	queued := make(chan error, 1)
	go func() { queued <- out.WriteMessageContext(queuedCtx, &protocol.DataRecordMsg{Value: []byte("queued")}) }()
	select {
	case err := <-queued:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued writer: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued writer ignored cancellation")
	}
	cancelQueued()
	waitState := func(paused bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for {
			out.mu.Lock()
			got := out.resume != nil
			out.mu.Unlock()
			if got == paused {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("control state %v blocked behind data", paused)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := in.ReportBufferUsage(80, 100); err != nil {
		t.Fatal(err)
	}
	waitState(true)
	if err := in.ReportBufferUsage(20, 100); err != nil {
		t.Fatal(err)
	}
	waitState(false)
	select {
	case err := <-done:
		t.Fatalf("oversized data escaped the window: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked write cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("data-window write ignored cancellation")
	}
}

func TestDataWindowWriteTimeout(t *testing.T) {
	t.Run("tcp", func(t *testing.T) { runTestDataWindowWriteTimeout(t, false) })
	t.Run("mutual_tls", func(t *testing.T) { runTestDataWindowWriteTimeout(t, true) })
}

func runTestDataWindowWriteTimeout(t *testing.T, secure bool) {
	server, client, addr := newTestMuxPairSecure(t, secure)
	out, err := client.Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	in, err := server.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out.cfg.ConnectionWriteTimeout = 40 * time.Millisecond
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		done <- out.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: make([]byte, 2*DefaultMaxStreamWindowSize)})
	}()
	select {
	case err := <-done:
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("window timeout: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream ignored caller deadline")
	}
	select {
	case <-out.done:
	default:
		t.Fatal("partially written frame left stream usable")
	}
}

func TestIncompatibleHandshakeWaitsForPeerToConsumeReply(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%t", secure), func(t *testing.T) {
			client, server := negotiationPair(t, secure)
			cfg := DefaultConfig()
			cfg.NodeID = "server"
			cfg.HandshakeTimeout = time.Second
			result := make(chan error, 1)
			go func() { _, err := server.NegotiateSession(context.Background(), cfg, false); result <- err }()
			stream, err := client.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.SetDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := protocol.EncodeAndWriteFrame(stream, &protocol.SessionHandshakeMsg{ProtocolVersion: 2, MinVersion: 2, NodeID: "client"}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				t.Fatalf("server closed before peer consumed reply: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			frame, err := protocol.ReadFrame(stream, cfg.MaxFrameSize)
			if err != nil {
				t.Fatal(err)
			}
			if frame.MsgType != protocol.MsgTypeSessionHandshake {
				t.Fatalf("reply type %d", frame.MsgType)
			}
			if _, err := protocol.DecodePayload(frame); err != nil {
				t.Fatal(err)
			}
			_ = client.Close()
			select {
			case err := <-result:
				if !errors.Is(err, protocol.ErrVersionIncompatible) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("server did not finish after peer close")
			}
		})
	}
}
