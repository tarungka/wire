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
	"github.com/tarungka/wire/internal/rpc"
)

func TestRejectedCheckpointReportUsesFailureBudget(t *testing.T) {
	a, b := net.Pipe()
	client, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	serverSession, err := yamux.Server(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := rpc.NewServer(rpc.DefaultConfig())
	server.Register(rpc.MethodAcknowledgeCheckpoint, func(context.Context, uint64, []byte) (any, *rpc.RPCError) {
		return &rpc.AcknowledgeCheckpointResponse{Accepted: false, Message: "checkpoint already aborted"}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, serverSession) }()
	defer func() { cancel(); _ = client.Close(); _ = serverSession.Close(); server.Stop(); <-done }()
	cfg := engine.DefaultTaskSlotConfig()
	cfg.Checkpoint.MaxConsecutiveFailures = 2
	w := New(Config{TaskSlot: &cfg, CheckpointReplica: &CheckpointReplicaConfig{StagingRoot: t.TempDir()}}, zerolog.Nop())
	w.client = rpc.NewClient(client, rpc.DefaultConfig())
	w.tasks["task"] = &taskHandle{checkpoint: &taskCheckpointRuntime{}}
	runtime, closeRuntime, err := w.prepareTaskCheckpoint(ctx, "job", "task", rpc.TaskDescriptor{EpochID: 1, CheckpointReplicaAddress: "unused:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	if err := runtime.report(ctx, 1, 1, nil); err != nil {
		t.Fatalf("first rejected report failed task: %v", err)
	}
	if err := runtime.report(ctx, 2, 1, nil); !errors.Is(err, engine.ErrMaxConsecutiveCheckpointFailures) {
		t.Fatalf("failure budget ignored: %v", err)
	}
}

func TestReconnectWaitIncludesConfiguredDrain(t *testing.T) {
	cfg := engine.DefaultTaskSlotConfig()
	cfg.DrainTimeout = 6 * time.Second
	w := New(Config{TaskSlot: &cfg}, zerolog.Nop())
	done := make(chan struct{})
	w.tasks["task"] = &taskHandle{cancel: func() {}, done: done}
	go func() { time.Sleep(5200 * time.Millisecond); close(done) }()
	if err := w.joinTasksForReconnect(); err != nil {
		t.Fatalf("configured drain interrupted reconnect: %v", err)
	}
}

func TestCheckpointReplicaReconnectsAfterPeerRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := CheckpointReplicaConfig{Authorize: func(context.Context, rpc.ReplicateCheckpointRequest) error { return nil }, AuthorizeFetch: func(context.Context, rpc.FetchCheckpointRequest) error { return nil }, ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 1}
	addr, stop, err := startCheckpointReplicaService(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	replica := archiveCheckpointReplicator{jobID: "job", taskID: "task", epoch: 1, stagingRoot: t.TempDir(), client: &reconnectingCheckpointClient{address: addr}}
	if err := replica.Replicate(ctx, engine.TaskCheckpoint{TaskID: "task", CheckpointID: 1, EpochID: 1}); err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	cfg.ListenAddr = addr
	_, stop, err = startCheckpointReplicaService(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := replica.Replicate(ctx, engine.TaskCheckpoint{TaskID: "task", CheckpointID: 2, EpochID: 1}); err != nil {
		t.Fatalf("next checkpoint did not reconnect: %v", err)
	}
}
