package worker

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

func TestCheckpointReplicaServicePublishesAndJoins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	addr, closeService, err := startCheckpointReplicaService(ctx, CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: root, ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 1, Authorize: func(_ context.Context, request rpc.ReplicateCheckpointRequest) error {
		if request.CheckpointID != 7 {
			return errors.New("checkpoint is not assigned")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeService()
	session, err := transport.NewClientSession(addr, transport.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	replica := archiveCheckpointReplicator{jobID: "job", taskID: "task", epoch: 2, stagingRoot: t.TempDir(), client: rpc.NewClient(session.YamuxSession(), rpc.DefaultConfig())}
	if err := replica.Replicate(ctx, engine.TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, Operators: [][]byte{[]byte("state")}}); err != nil {
		t.Fatal(err)
	}
	store, err := engine.NewFileCheckpointStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "job", "task", 7, 2); err != nil {
		t.Fatal(err)
	}
	if err := replica.Replicate(ctx, engine.TaskCheckpoint{TaskID: "task", CheckpointID: 8, EpochID: 2}); err == nil {
		t.Fatal("unauthorized checkpoint acknowledged")
	}
	if _, err := store.Get(ctx, "job", "task", 8, 2); !os.IsNotExist(err) {
		t.Fatalf("unauthorized checkpoint published: %v", err)
	}
	done := make(chan struct{})
	go func() { closeService(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("replica service did not join idle connection")
	}
}
