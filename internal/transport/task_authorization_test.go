package transport

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestAuthenticatedTaskSourceOwnership(t *testing.T) {
	certs := generateTestCerts(t)
	serverTLS, err := LoadTLSConfig(certs.ServerCertFile, certs.ServerKeyFile, true, certs.CACertFile)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := NewTLSClientConfig(certs.ClientCertFile, certs.ClientKeyFile, certs.CACertFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := DefaultConfig()
	cfg.ListenAddr, cfg.NodeID, cfg.TLSConfig, cfg.RequirePeerIdentity = "127.0.0.1:0", "localhost", serverTLS, true
	server := NewMux(cfg)
	if err := server.Listen(ctx); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	cfg.ListenAddr, cfg.NodeID, cfg.TLSConfig = "", "client", clientTLS
	client := NewMux(cfg)
	defer client.Close()
	if err := server.RegisterTaskSources("missing-owner", []TaskSource{{TaskID: "source"}}); err == nil {
		t.Fatal("secure task admitted missing ownership")
	}
	if server.IsTaskRegistered("missing-owner") {
		t.Fatal("invalid policy installed")
	}
	if err := server.RegisterTaskSources("sink", []TaskSource{{TaskID: "source", WorkerID: "client", PartitionIndex: 1}, {TaskID: "other-source", WorkerID: "other-worker"}}); err != nil {
		t.Fatal(err)
	}
	for _, header := range []protocol.StreamHeaderMsg{
		{SourceTaskID: "other-source", TargetTaskID: "sink"},
		{SourceTaskID: "source", TargetTaskID: "sink", PartitionIndex: 2},
		{SourceTaskID: "unknown-source", TargetTaskID: "sink"},
	} {
		stream, err := client.Dial(ctx, server.ListenAddr(), header)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-stream.senderReadDone:
		case <-ctx.Done():
			t.Fatal("forged source was not rejected")
		}
		server.mu.RLock()
		queued := len(server.tasks["sink"].streams)
		server.mu.RUnlock()
		if queued != 0 {
			t.Fatal("unauthorized stream consumed task input capacity")
		}
	}
	sender, err := client.Dial(ctx, server.ListenAddr(), protocol.StreamHeaderMsg{SourceTaskID: "source", TargetTaskID: "sink", PartitionIndex: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	receiver, err := server.AcceptTask(ctx, "sink")
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	if sender.session.IsClosed() {
		t.Fatal("rejection closed the shared session")
	}
	server.UnregisterTask("sink")
	if err := server.RegisterTaskSources("sink", []TaskSource{{TaskID: "source", WorkerID: "new-owner", PartitionIndex: 1}}); err != nil {
		t.Fatal(err)
	}
	stale, err := client.Dial(ctx, server.ListenAddr(), protocol.StreamHeaderMsg{SourceTaskID: "source", TargetTaskID: "sink", PartitionIndex: 1})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-stale.senderReadDone:
	case <-ctx.Done():
		t.Fatal("replacement task retained previous ownership")
	}
}
