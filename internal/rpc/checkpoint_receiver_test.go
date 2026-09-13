package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/engine"
)

func TestCheckpointReceiverPublishesBeforeReceipt(t *testing.T) {
	for _, mode := range []string{"valid", "checksum", "identity"} {
		t.Run(mode, func(t *testing.T) {
			root, staging := t.TempDir(), t.TempDir()
			store, err := engine.NewFileCheckpointStore(root)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewCheckpointReplicaHandler(staging, 1, func(ctx context.Context, r ReplicateCheckpointRequest, body io.Reader) error {
				return store.Import(ctx, r.JobID, r.TaskID, r.CheckpointID, r.EpochID, body)
			})
			if err != nil {
				t.Fatal(err)
			}
			clientSession, serverSession := testYamuxPair(t)
			server := NewServer(DefaultConfig())
			handled := make(chan struct{})
			server.RegisterStream(MethodReplicateCheckpoint, func(ctx context.Context, id uint64, payload []byte, stream *yamux.Stream) error {
				defer close(handled)
				return handler(ctx, id, payload, stream)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			go server.ServeSession(ctx, serverSession)
			snapshot := engine.TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, Operators: [][]byte{[]byte("state")}}
			payload, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			request := ReplicateCheckpointRequest{JobID: "job", TaskID: "task", CheckpointID: 7, EpochID: 2, Size: uint64(len(payload)), SHA256: sha256.Sum256(payload)}
			if mode == "checksum" {
				request.SHA256[0] ^= 1
			}
			if mode == "identity" {
				request.TaskID = "wrong"
			}
			err = NewClient(clientSession, DefaultConfig()).ReplicateCheckpoint(ctx, request, bytes.NewReader(payload))
			if (err == nil) != (mode == "valid") {
				t.Fatalf("transfer: %v", err)
			}
			select {
			case <-handled:
			case <-ctx.Done():
				t.Fatal("receiver did not exit")
			}
			pending, err := os.ReadDir(staging)
			if err != nil || len(pending) != 0 {
				t.Fatalf("staging leaked: %v %v", pending, err)
			}
			if mode == "valid" {
				reopened, err := engine.NewFileCheckpointStore(root)
				if err != nil {
					t.Fatal(err)
				}
				restored, err := reopened.Get(ctx, "job", "task", 7, 2)
				if err != nil || len(restored.Operators) != 1 || string(restored.Operators[0]) != "state" {
					t.Fatalf("receipt without recoverable snapshot: %+v %v", restored, err)
				}
			} else {
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Fatalf("invalid snapshot published: %v %v", entries, err)
				}
			}
		})
	}
}
