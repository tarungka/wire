package transport

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestSessionDrainRequiresNegotiatedFeature(t *testing.T) {
	client, server := negotiationPair(t)
	cfg := DefaultConfig()
	cfg.NodeID = "older-worker"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := server.NegotiateSession(ctx, cfg, false); result <- err }()
	if _, err := client.NegotiateSession(ctx, cfg, true); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	client.beginDrain(cfg)
	if client.isDraining() {
		t.Fatal("unnegotiated retirement started")
	}
	output, err := client.OpenDataStream(ctx, cfg, protocol.StreamHeaderMsg{SourceTaskID: "s", TargetTaskID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	input, err := server.AcceptDataStream(cfg, func(protocol.StreamHeaderMsg) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := output.WriteMessage(&protocol.DataRecordMsg{Value: []byte("compatible")}); err != nil {
		t.Fatal(err)
	}
	if _, err := input.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	// A peer that sends the extension despite not negotiating it is rejected.
	// The server can reject the frame and close Yamux before the sender's
	// write returns. Either write result is valid; peer shutdown is the
	// protocol outcome being tested.
	writeErr := protocol.EncodeAndWriteFrame(client.control, &protocol.SessionDrainMsg{})
	select {
	case <-server.yamux.CloseChan():
	case <-ctx.Done():
		t.Fatalf("peer did not reject unnegotiated drain (write error: %v)", writeErr)
	}
}
