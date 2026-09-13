package worker

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"reflect"
	"testing"

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
