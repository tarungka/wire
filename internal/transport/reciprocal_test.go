package transport

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestMuxAcceptsPeerOpenedStreamsOnOutboundSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	server := NewMux(cfg)
	client := NewMux(cfg)
	defer server.Close()
	defer client.Close()
	if err := server.Listen(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.RegisterTask("reverse"); err != nil {
		t.Fatal(err)
	}
	first, err := client.Dial(ctx, server.ListenAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	incoming, err := server.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer incoming.Close()
	// Open from the accepting side of the existing connection. No second TCP
	// connection or handshake is needed for the opposite data direction.
	reverse, err := incoming.session.OpenDataStream(ctx, server.cfg, protocol.StreamHeaderMsg{SourceTaskID: "source", TargetTaskID: "reverse", PartitionIndex: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer reverse.Close()
	received, err := client.AcceptTask(ctx, "reverse")
	if err != nil {
		t.Fatal(err)
	}
	defer received.Close()
	if received.session != first.session {
		t.Fatal("reverse stream used another session")
	}
	if err := reverse.WriteMessage(&protocol.DataRecordMsg{Value: []byte("reverse record")}); err != nil {
		t.Fatal(err)
	}
	message, err := received.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(message.(*protocol.DataRecordMsg).Value) != "reverse record" {
		t.Fatal("record changed")
	}
	header, ok := received.Header()
	if !ok || header.PartitionIndex != 2 {
		t.Fatal("routing header lost")
	}
	if err := reverse.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "source"}); err != nil {
		t.Fatal(err)
	}
	if _, err := received.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	// The original direction stays usable after the reverse stream ends.
	if err := first.WriteMessage(&protocol.DataRecordMsg{Value: []byte("forward record")}); err != nil {
		t.Fatal(err)
	}
	if _, err := incoming.ReadMessage(); err != nil {
		t.Fatal(err)
	}
}

func TestMuxReciprocalDialReusesAdvertisedEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	a, b := NewMux(cfg), NewMux(cfg)
	defer a.Close()
	defer b.Close()
	for _, mux := range []*Mux{a, b} {
		if err := mux.Listen(ctx); err != nil {
			t.Fatal(err)
		}
	}
	forward, err := a.Dial(ctx, b.ListenAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer forward.Close()
	input, err := b.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	reverse, err := b.Dial(ctx, a.ListenAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer reverse.Close()
	if reverse.session != input.session {
		t.Fatal("reciprocal dial created another session")
	}
	reverseInput, err := a.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reverseInput.Close()
	if reverseInput.session != forward.session {
		t.Fatal("receiver used another session")
	}
	for _, mux := range []*Mux{a, b} {
		mux.mu.RLock()
		count := len(mux.sessions)
		mux.mu.RUnlock()
		if count != 1 {
			t.Fatalf("got %d connections", count)
		}
	}
}

func TestMuxCrossedConnectionsSelectSameSession(t *testing.T) {
	t.Run("tcp", func(t *testing.T) { testMuxCrossedConnections(t, nil) })
	t.Run("mutual_tls", func(t *testing.T) {
		certs := generateTestCerts(t)
		cfg, err := LoadTLSConfig(certs.ServerCertFile, certs.ServerKeyFile, true, certs.CACertFile)
		if err != nil {
			t.Fatal(err)
		}
		// Each worker acts as both TLS client and server.
		cfg.RootCAs = cfg.ClientCAs
		testMuxCrossedConnections(t, cfg)
	})
}

func testMuxCrossedConnections(t *testing.T, tlsConfig *tls.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := DefaultConfig()
	cfg.TLSConfig = tlsConfig
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.NodeID = "a"
	a := NewMux(cfg)
	cfg.NodeID = "b"
	b := NewMux(cfg)
	defer a.Close()
	defer b.Close()
	for _, mux := range []*Mux{a, b} {
		if err := mux.Listen(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Establish both TCP connections before either handshake. This forces the
	// crossed-connection case even on a single CPU or a slow test runner.
	ac, err := NewClientSessionContext(ctx, b.ListenAddr(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ac.Close()
	bc, err := NewClientSessionContext(ctx, a.ListenAddr(), b.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	var held, heldInput *FrameStream
	for _, pair := range []struct {
		mux     *Mux
		session *Session
		addr    string
	}{{b, bc, a.ListenAddr()}, {a, ac, b.ListenAddr()}} {
		if _, err := pair.session.NegotiateSession(ctx, pair.mux.sessionConfig(), true); err != nil {
			t.Fatal(err)
		}
		pair.mux.mu.Lock()
		pair.mux.publishSession(pair.addr, pair.session)
		pair.mux.wg.Add(1)
		pair.mux.mu.Unlock()
		go func(mux *Mux, sess *Session) {
			defer mux.wg.Done()
			defer mux.forgetSession(sess)
			mux.sessionAcceptLoop(mux.ctx, sess)
		}(pair.mux, pair.session)
		if pair.mux == b {
			held, err = bc.OpenDataStream(ctx, b.cfg, protocol.StreamHeaderMsg{SourceTaskID: "held", TargetTaskID: "default"})
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			heldInput, err = a.Accept(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer heldInput.Close()
		}
	}
	// Accepting-side publication follows its handshake write, so synchronize
	// with it using a routed data stream on each forced connection.
	for _, pair := range []struct {
		session *Session
		cfg     Config
		target  *Mux
	}{{ac, a.cfg, b}} {
		stream, err := pair.session.OpenDataStream(ctx, pair.cfg, protocol.StreamHeaderMsg{SourceTaskID: "s", TargetTaskID: "default"})
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		input, err := pair.target.Accept(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		if err := stream.WriteMessage(&protocol.DataRecordMsg{Value: []byte("existing")}); err != nil {
			t.Fatal(err)
		}
		if _, err := input.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	if err := held.WriteMessage(&protocol.DataRecordMsg{Value: []byte("survives drain")}); err != nil {
		t.Fatal(err)
	}
	message, err := heldInput.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(message.(*protocol.DataRecordMsg).Value) != "survives drain" {
		t.Fatal("held record changed")
	}
	if bc.IsClosed() {
		t.Fatal("duplicate closed with a live stream")
	}
	if err := held.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "held"}); err != nil {
		t.Fatal(err)
	}
	if _, err := heldInput.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		a.mu.RLock()
		an := len(a.sessions)
		a.mu.RUnlock()
		b.mu.RLock()
		bn := len(b.sessions)
		b.mu.RUnlock()
		if an == 1 && bn == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("duplicates did not drain: %d/%d", an, bn)
		case <-ticker.C:
		}
	}
	a.mu.RLock()
	as := a.nodes["b"]
	a.mu.RUnlock()
	b.mu.RLock()
	bs := b.nodes["a"]
	b.mu.RUnlock()
	if as != ac || bs.initiator {
		t.Fatal("workers selected different initiators")
	}
	if as.conn.LocalAddr().String() != bs.conn.RemoteAddr().String() {
		t.Fatal("workers selected different connections")
	}
	reverse, err := b.Dial(ctx, a.ListenAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer reverse.Close()
	if reverse.session != bs {
		t.Fatal("later dial did not use selected connection")
	}
	received, err := a.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer received.Close()
	if received.session != as {
		t.Fatal("later dial arrived on another connection")
	}
}

func TestMuxConcurrentReciprocalDialDrainsDuplicates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.NodeID = "a"
	a := NewMux(cfg)
	cfg.NodeID = "b"
	b := NewMux(cfg)
	defer a.Close()
	defer b.Close()
	for _, mux := range []*Mux{a, b} {
		if err := mux.Listen(ctx); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		stream *FrameStream
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, pair := range []struct{ from, to *Mux }{{a, b}, {b, a}} {
		go func(from, to *Mux) {
			<-start
			stream, err := from.Dial(ctx, to.ListenAddr())
			results <- result{stream, err}
		}(pair.from, pair.to)
	}
	close(start)
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		defer r.stream.Close()
		if err := r.stream.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "done"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, mux := range []*Mux{a, b} {
		stream, err := mux.Accept(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		if _, err := stream.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		a.mu.RLock()
		an := len(a.sessions)
		a.mu.RUnlock()
		b.mu.RLock()
		bn := len(b.sessions)
		b.mu.RUnlock()
		if an == 1 && bn == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("connection counts %d/%d", an, bn)
		case <-tick.C:
		}
	}
}

func TestMuxSelfDialKeepsBothConnectionEndpoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	mux := NewMux(cfg)
	defer mux.Close()
	if err := mux.Listen(ctx); err != nil {
		t.Fatal(err)
	}
	if err := mux.RegisterTask("local-target"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		output, err := mux.Dial(ctx, mux.ListenAddr(), protocol.StreamHeaderMsg{SourceTaskID: "local-source", TargetTaskID: "local-target"})
		if err != nil {
			t.Fatal(err)
		}
		mux.mu.RLock()
		draining := false
		for sess := range mux.sessions {
			if sess.isDraining() {
				draining = true
			}
		}
		mux.mu.RUnlock()
		if draining {
			t.Fatal("retiring one endpoint of the loopback connection")
		}
		input, err := mux.AcceptTask(ctx, "local-target")
		if err != nil {
			t.Fatal(err)
		}
		if err := output.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: []byte("local")}); err != nil {
			t.Fatal(err)
		}
		message, err := input.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(message.(*protocol.DataRecordMsg).Value) != "local" {
			t.Fatal("record changed")
		}
		if err := output.WriteMessageContext(ctx, &protocol.EndOfPartitionMsg{SourceID: "local-source"}); err != nil {
			t.Fatal(err)
		}
		if _, err := input.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	mux.mu.RLock()
	count := len(mux.sessions)
	mux.mu.RUnlock()
	if count != 2 {
		t.Fatalf("one loopback TCP connection needs two local session endpoints, got %d", count)
	}
}
