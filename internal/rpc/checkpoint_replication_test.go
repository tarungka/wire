package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

func TestCheckpointReplicaReceipt(t *testing.T) {
	for _, mode := range []string{"stored", "wrong-identity", "wrong-request", "not-stored"} {
		t.Run(mode, func(t *testing.T) {
			clientSession, serverSession := testYamuxPair(t)
			cfg := DefaultConfig()
			server := NewServer(cfg)
			received := make(chan struct{})
			release := make(chan struct{})
			server.RegisterStream(MethodReplicateCheckpoint, func(ctx context.Context, id uint64, payload []byte, stream *yamux.Stream) error {
				var request ReplicateCheckpointRequest
				if err := DecodeRPCPayload(RPCFrame{Payload: payload}, &request); err != nil {
					return err
				}
				if err := request.Validate(); err != nil {
					return err
				}
				if err := EncodeRPCRequest(stream, MethodReplicateCheckpoint, id, CheckpointReplicaAdmission{Accepted: true}); err != nil {
					return err
				}
				if err := ReadCheckpointChunks(ctx, stream, id, request.Size, request.SHA256, io.Discard); err != nil {
					return err
				}
				close(received)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				receipt := CheckpointReplicaReceipt{Stored: true, Snapshot: request}
				switch mode {
				case "wrong-identity":
					receipt.Snapshot.EpochID++
				case "wrong-request":
					id++
				case "not-stored":
					receipt.Stored = false
				}
				return EncodeRPCRequest(stream, MethodReplicateCheckpoint, id, receipt)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			go server.ServeSession(ctx, serverSession)
			body := bytes.Repeat([]byte{42}, CheckpointChunkSize+1)
			request := ReplicateCheckpointRequest{JobID: "job", TaskID: "task", CheckpointID: 7, EpochID: 2, Size: uint64(len(body)), SHA256: sha256.Sum256(body)}
			client := NewClient(clientSession, cfg)
			done := make(chan error, 1)
			go func() { done <- client.ReplicateCheckpoint(ctx, request, bytes.NewReader(body)) }()
			select {
			case <-received:
			case <-ctx.Done():
				t.Fatal("body not received")
			}
			select {
			case err := <-done:
				t.Fatalf("completed before receipt: %v", err)
			default:
			}
			close(release)
			select {
			case err := <-done:
				if (err == nil) != (mode == "stored") {
					t.Fatalf("receipt result: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("receipt did not complete transfer")
			}
		})
	}
}
