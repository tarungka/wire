package worker

import (
	"bytes"
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
	addr, closeService, err := startCheckpointReplicaService(ctx, CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: root, ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 1, AuthorizeFetch: func(_ context.Context, request rpc.FetchCheckpointRequest) error {
		if request.TargetJobID != "" && (request.TargetJobID != "new-job" || request.TargetTaskID != "new-task" || request.JobID != "job" || request.TaskID != "task") {
			return errors.New("wrong upgrade identities")
		}
		if request.WorkerID != "recovery-worker" {
			return errors.New("unassigned recovery")
		}
		return nil
	}, Authorize: func(_ context.Context, request rpc.ReplicateCheckpointRequest) error {
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

	client := rpc.NewClient(session.YamuxSession(), rpc.DefaultConfig())
	request := rpc.FetchCheckpointRequest{WorkerID: "recovery-worker", DeploymentEpoch: 3, JobID: "job", TaskID: "task", CheckpointID: 7, EpochID: 2}
	var fetched bytes.Buffer
	if err := client.FetchCheckpoint(ctx, request, &fetched); err != nil {
		t.Fatal(err)
	}
	restored, err := engine.NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.ImportArchive(ctx, "job", "task", 7, 2, bytes.NewReader(fetched.Bytes()), t.TempDir(), int64(fetched.Len())); err != nil {
		t.Fatal(err)
	}
	snapshot, err := restored.Get(ctx, "job", "task", 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Operators) != 1 || string(snapshot.Operators[0]) != "state" {
		t.Fatalf("restored: %+v", snapshot)
	}
	recovery := &Worker{cfg: Config{WorkerID: "recovery-worker", CheckpointReplica: &CheckpointReplicaConfig{StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir()}}}
	recovered, err := recovery.fetchTaskCheckpoint(ctx, "job", "task", rpc.TaskDescriptor{EpochID: 3, RestoreCheckpoint: &rpc.CheckpointRestoreDescriptor{CheckpointID: 7, EpochID: 2, ReplicaAddress: addr}})
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Operators) != 1 || string(recovered.Operators[0]) != "state" {
		t.Fatalf("worker recovery: %+v", recovered)
	}
	upgraded, err := recovery.fetchTaskCheckpoint(ctx, "new-job", "new-task", rpc.TaskDescriptor{EpochID: 3, RestoreCheckpoint: &rpc.CheckpointRestoreDescriptor{SourceJobID: "job", SourceTaskID: "task", CheckpointID: 7, EpochID: 2, ReplicaAddress: addr}})
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.TaskID != "task" || string(upgraded.Operators[0]) != "state" {
		t.Fatalf("upgrade rewrote archive identity or state: %+v", upgraded)
	}
	request.WorkerID = "unassigned"
	fetched.Reset()
	if err := client.FetchCheckpoint(ctx, request, &fetched); err == nil || fetched.Len() != 0 {
		t.Fatal("unauthorized recovery received state")
	}
	done := make(chan struct{})
	go func() { closeService(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("replica service did not join idle connection")
	}
}
