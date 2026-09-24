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

type checkpointBodyProbe struct{ reads int }

func (p *checkpointBodyProbe) Read([]byte) (int, error) { p.reads++; return 0, io.EOF }

func TestCheckpointReceiverRejectsBeforeBody(t *testing.T) {
	staging := t.TempDir()
	publishing, release := make(chan struct{}), make(chan struct{})
	handler, err := NewCheckpointReplicaHandler(staging, 1, func(ctx context.Context, _ ReplicateCheckpointRequest, _ io.Reader) error {
		close(publishing)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	clientSession, serverSession := testYamuxPair(t)
	server := NewServer(DefaultConfig())
	handled := make(chan struct{}, 2)
	server.RegisterStream(MethodReplicateCheckpoint, func(ctx context.Context, id uint64, payload []byte, stream *yamux.Stream) error {
		defer func() { handled <- struct{}{} }()
		return handler(ctx, id, payload, stream)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go server.ServeSession(ctx, serverSession)
	client := NewClient(clientSession, DefaultConfig())
	request := ReplicateCheckpointRequest{JobID: "job", TaskID: "task", CheckpointID: 1, Size: 1, SHA256: sha256.Sum256([]byte{42})}
	first := make(chan error, 1)
	go func() { first <- client.ReplicateCheckpoint(ctx, request, bytes.NewReader([]byte{42})) }()
	select {
	case <-publishing:
	case <-ctx.Done():
		t.Fatal("first transfer not publishing")
	}
	probe := &checkpointBodyProbe{}
	rejected, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stop()
	if err := client.ReplicateCheckpoint(rejected, request, probe); err == nil {
		t.Fatal("excess transfer admitted")
	}
	if rejected.Err() != nil {
		t.Fatal("rejection waited for timeout")
	}
	if probe.reads != 0 {
		t.Fatal("rejected transfer read its body")
	}
	close(release)
	select {
	case err := <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("admitted transfer failed to finish")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-handled:
		case <-ctx.Done():
			t.Fatal("handler failed to exit")
		}
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging: %v %v", entries, err)
	}
}
