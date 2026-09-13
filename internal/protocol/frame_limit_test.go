package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestOutgoingFrameLimit(t *testing.T) {
	messages := []any{
		&StreamHeaderMsg{SourceTaskID: "source", TargetTaskID: "target"},
		&SessionHandshakeMsg{ProtocolVersion: 1, MinVersion: 1, NodeID: "worker"},
		&DataRecordMsg{Value: bytes.Repeat([]byte("x"), 1024)},
		&CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1},
		&WatermarkMsg{SourceID: "source"},
		&EndOfPartitionMsg{SourceID: "source"},
		&BackpressureMsg{StreamID: 1, State: BackpressurePause},
	}
	for _, msg := range messages {
		payload, err := EncodeMsgPack(msg)
		if err != nil {
			t.Fatal(err)
		}
		limit := uint32(len(payload)) + MinFrameLength
		var wire bytes.Buffer
		if err := EncodeAndWriteFrameLimit(&wire, msg, limit-1); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("%T: got %v", msg, err)
		}
		if wire.Len() != 0 {
			t.Fatalf("%T: rejected frame wrote %d bytes", msg, wire.Len())
		}
		if err := EncodeAndWriteFrameLimit(&wire, msg, limit); err != nil {
			t.Fatalf("%T: %v", msg, err)
		}
		frame, err := ReadFrame(&wire, limit)
		if err != nil {
			t.Fatal(err)
		}
		if frame.Length != limit || !bytes.Equal(frame.Payload, payload) {
			t.Fatalf("%T: boundary frame changed", msg)
		}
	}
}
