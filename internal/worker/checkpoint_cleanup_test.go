package worker

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCleanupCommandDeletesBeforeReceiptAndRetries(t *testing.T) {
	root := t.TempDir()
	store, err := engine.NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), "job", engine.TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2}); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	caller, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := yamux.Server(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(rpc.DefaultConfig())
	receipts := make(chan struct{}, 2)
	server.Register(rpc.MethodAcknowledgeCheckpointCleanup, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var req rpc.CheckpointCleanupRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			t.Error(err)
		}
		if _, err := store.Get(t.Context(), req.JobID, req.TaskID, req.CheckpointID, req.SnapshotEpoch); !errors.Is(err, engine.ErrCheckpointDeleted) {
			t.Errorf("receipt preceded deletion: %v", err)
		}
		receipts <- struct{}{}
		return &rpc.AcknowledgeCheckpointResponse{Accepted: true}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, peer) }()
	defer func() { cancel(); _ = caller.Close(); _ = peer.Close(); server.Stop(); <-done }()
	w := New(Config{WorkerID: "replica", CheckpointReplica: &CheckpointReplicaConfig{StoreRoot: root}}, zerolog.Nop())
	w.epoch = 5
	w.client = rpc.NewClient(caller, rpc.DefaultConfig())
	stop := w.startCheckpointCleanup(ctx)
	defer stop()
	req := rpc.CheckpointCleanupRequest{WorkerID: "replica", EpochID: 5, SnapshotEpoch: 2, JobID: "job", TaskID: "task", CheckpointID: 7, SavepointID: "save"}
	raw, err := protocol.EncodeMsgPack(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := rpc.WorkerCommand{Type: rpc.CommandTypeDeleteCheckpoint, JobID: "job", TaskID: "task", EpochID: 5, Data: raw}
	stale := cmd
	stale.EpochID = 4
	if err := w.deleteCheckpointReplica(ctx, stale); err == nil {
		t.Fatal("stale cleanup admitted")
	}
	if _, err := store.Get(ctx, "job", "task", 7, 2); err != nil {
		t.Fatal("stale cleanup deleted state", err)
	}
	for range 2 {
		w.handleCommands([]rpc.WorkerCommand{cmd})
		select {
		case <-receipts:
		case <-time.After(5 * time.Second):
			t.Fatal("cleanup receipt missing")
		}
	}
}
