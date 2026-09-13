package transport

import (
	"context"
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
