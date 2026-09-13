package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestArchiveCheckpointReplicationPublication(t *testing.T) {
	ctx := context.Background()
	store, err := engine.NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts := t.TempDir()
	staging := t.TempDir()
	snapshot := engine.TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, HasSource: true, Source: []byte("offset"), Operators: [][]byte{[]byte("state")}}
	r := archiveCheckpointReplicator{jobID: "job", taskID: "task", epoch: 2, stagingRoot: staging, client: checkpointReplicaClientFunc(func(ctx context.Context, request rpc.ReplicateCheckpointRequest, body io.Reader) error {
		if request.Format != rpc.CheckpointFormatArchive {
			t.Fatal("wrong transfer format")
		}
		hash := sha256.New()
		if err := publishCheckpointReplica(ctx, store, artifacts, request, io.TeeReader(body, hash)); err != nil {
			return err
		}
		var digest [32]byte
		copy(digest[:], hash.Sum(nil))
		if digest != request.SHA256 {
			t.Fatal("package digest mismatch")
		}
		return nil
	})}
	for range 2 {
		if err := r.Replicate(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	received, err := store.Get(ctx, "job", "task", 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received, snapshot) {
		t.Fatalf("changed snapshot: %+v", received)
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging leak: %v, %v", entries, err)
	}
}

func TestArchiveReplicaRPCRecoversPebbleWithoutOrigin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	origin := t.TempDir()
	backend, err := engine.NewStateBackend(engine.StateBackendConfig{Type: engine.StateBackendPebble, PebbleDataDir: origin})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	if err := backend.Put([]byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	handle, err := backend.Checkpoint(7)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(handle)
	if err != nil {
		t.Fatal(err)
	}
	store, err := engine.NewFileCheckpointStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts := t.TempDir()
	handler, err := rpc.NewCheckpointReplicaHandler(t.TempDir(), 1, func(ctx context.Context, request rpc.ReplicateCheckpointRequest, body io.Reader) error {
		return publishCheckpointReplica(ctx, store, artifacts, request, body)
	})
	if err != nil {
		t.Fatal(err)
	}
	left, right := net.Pipe()
	clientSession, err := yamux.Client(left, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	serverSession, err := yamux.Server(right, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	server := rpc.NewServer(rpc.DefaultConfig())
	server.RegisterStream(rpc.MethodReplicateCheckpoint, handler)
	joined := make(chan struct{})
	go func() { defer close(joined); server.ServeSession(ctx, serverSession) }()
	defer func() { cancel(); _ = serverSession.Close(); <-joined }()
	r := archiveCheckpointReplicator{jobID: "job", taskID: "task", epoch: 2, stagingRoot: t.TempDir(), client: rpc.NewClient(clientSession, rpc.DefaultConfig())}
	snapshot := engine.TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, Operators: [][]byte{encoded}, StateHandleIndexes: []int{0}}
	if err := r.Replicate(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	received, err := store.Get(ctx, "job", "task", 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	var relocated engine.SnapshotHandle
	if err := json.Unmarshal(received.Operators[0], &relocated); err != nil {
		t.Fatal(err)
	}
	restored, err := engine.NewStateBackend(engine.StateBackendConfig{Type: engine.StateBackendPebble, PebbleDataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Restore(relocated); err != nil {
		t.Fatal(err)
	}
	value, err := restored.Get([]byte("key"))
	if err != nil || string(value) != "value" {
		t.Fatalf("remote recovery: %q, %v", value, err)
	}
}
