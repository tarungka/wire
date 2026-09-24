package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/tarungka/wire/internal/config"
	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/transport"
)

func TestWorkerHonorsConfiguredFrameLimit(t *testing.T) {
	cfg := config.DefaultConfig()
	flags := pflag.NewFlagSet("node", pflag.ContinueOnError)
	flags.Uint32("max-frame-size", 16777216, "")
	if err := flags.Parse([]string{"--max-frame-size", "4096"}); err != nil {
		t.Fatal(err)
	}
	if err := config.ApplyFlags(&cfg, flags); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	coord := coordinator.New(coordinator.CoordinatorConfig{}, coordinator.NewMemoryStore(), nil, zerolog.Nop())
	cd := make(chan error, 1)
	go func() { cd <- coord.Run(ctx) }()
	defer func() { cancel(); <-cd }()
	for !coord.IsReady() {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	sd := make(chan error, 1)
	go func() { sd <- server.Serve(ctx) }()
	defer func() { _ = server.Shutdown(context.Background()); <-sd }()
	w := New(Config{WorkerID: "frame-worker", CoordinatorAddr: server.Addr(), ListenAddr: "127.0.0.1:0", TaskSlots: 1, MaxFrameSize: cfg.MaxFrameSize}, zerolog.Nop())
	wd := make(chan error, 1)
	go func() { wd <- w.Run(ctx) }()
	defer func() { cancel(); _ = w.Shutdown(context.Background()); <-wd }()
	for len(coord.ListWorkers()) != 1 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	w.mu.RLock()
	data := w.data
	w.mu.RUnlock()
	peerCfg := transport.DefaultConfig()
	peerCfg.NodeID = "peer"
	peerCfg.ListenAddr = "127.0.0.1:0"
	peer := transport.NewMux(peerCfg)
	if err := peer.Listen(ctx); err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := peer.RegisterTask("target"); err != nil {
		t.Fatal(err)
	}
	stream, err := data.Dial(ctx, peer.ListenAddr(), protocol.StreamHeaderMsg{SourceTaskID: "source", TargetTaskID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	receiver, err := peer.AcceptTask(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	if err := stream.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: []byte("small")}); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if err := stream.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: make([]byte, 4096)}); !errors.Is(err, protocol.ErrFrameTooLarge) {
		t.Fatalf("configured limit did not reject oversized record: %v", err)
	}
	if err := data.RegisterTask("incoming"); err != nil {
		t.Fatal(err)
	}
	sender, err := peer.Dial(ctx, data.ListenAddr(), protocol.StreamHeaderMsg{SourceTaskID: "remote", TargetTaskID: "incoming"})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	incoming, err := data.AcceptTask(ctx, "incoming")
	if err != nil {
		t.Fatal(err)
	}
	defer incoming.Close()
	if err := sender.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: make([]byte, 4096)}); err != nil {
		t.Fatal(err)
	}
	if _, err := incoming.ReadMessage(); !errors.Is(err, protocol.ErrFrameTooLarge) {
		t.Fatalf("configured receive limit ignored: %v", err)
	}

}
