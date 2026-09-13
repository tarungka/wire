package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

func TestCheckpointTransferExceedsUnaryLimit(t *testing.T) {
	data := bytes.Repeat([]byte("snapshot"), (MaxRPCPayloadSize/8)+1)
	var wire, restored bytes.Buffer
	if err := WriteCheckpointChunks(context.Background(), &wire, 17, bytes.NewReader(data), uint64(len(data))); err != nil {
		t.Fatal(err)
	}
	if err := ReadCheckpointChunks(context.Background(), &wire, 17, uint64(len(data)), sha256.Sum256(data), &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, restored.Bytes()) || wire.Len() != 0 {
		t.Fatal("transfer corrupted or left unread bytes")
	}
}

func TestCheckpointTransferRejectsInvalidChunks(t *testing.T) {
	for _, mutation := range []string{"method", "request", "offset", "empty", "size", "checksum"} {
		t.Run(mutation, func(t *testing.T) {
			payload := make([]byte, 9)
			payload[8] = 42
			frame := RPCFrame{MethodID: MethodReplicateCheckpoint, RequestID: 7, Payload: payload}
			size := uint64(1)
			digest := sha256.Sum256([]byte{42})
			switch mutation {
			case "method":
				frame.MethodID = MethodHeartbeat
			case "request":
				frame.RequestID++
			case "offset":
				binary.BigEndian.PutUint64(payload[:8], 1)
			case "empty":
				frame.Payload = payload[:8]
			case "size":
				frame.Payload = append(payload, 43)
			case "checksum":
				digest[0] ^= 1
			}
			var wire, staging bytes.Buffer
			if err := WriteRPCFrame(&wire, frame); err != nil {
				t.Fatal(err)
			}
			if err := ReadCheckpointChunks(context.Background(), &wire, 7, size, digest, &staging); err == nil {
				t.Fatal("invalid transfer accepted")
			}
		})
	}
}
