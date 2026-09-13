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
		&SessionDrainMsg{Ready: true},
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

func TestPayloadBufferRejectsBeforeAllocation(t *testing.T) {
	b := payloadBuffer{limit: 32}
	huge := make([]byte, 16*1024*1024)
	if n, err := b.Write(huge); n != 0 || !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %d, %v", n, err)
	}
	if b.buffer.Cap() != 0 {
		t.Fatalf("rejected write allocated %d bytes", b.buffer.Cap())
	}
	if _, err := encodeMsgPackLimit(&DataRecordMsg{Value: huge}, 32); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("encoder: %v", err)
	}
	var wire bytes.Buffer
	for _, limit := range []uint32{0, MinFrameLength - 1, MinFrameLength} {
		if err := EncodeAndWriteFrameLimit(&wire, &DataRecordMsg{}, limit); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	if wire.Len() != 0 {
		t.Fatal("invalid limit wrote bytes")
	}
}

func TestFrameRejectsTrailingPayload(t *testing.T) {
	for _, mt := range []uint8{MsgTypeStreamHeader, MsgTypeSessionHandshake, MsgTypeDataRecord, MsgTypeCheckpointBarrier, MsgTypeWatermark, MsgTypeEndOfPartition, MsgTypeBackpressure} {
		// An empty map is structurally decodable into each message, but a second
		// object or malformed tail must never be hidden by that successful decode.
		for _, tail := range []byte{0x80, 0xc0, 0xc1} {
			_, err := DecodePayload(Frame{MsgType: mt, Payload: []byte{0x80, tail}})
			if !errors.Is(err, ErrDecodePayload) {
				t.Fatalf("type %d tail %x: %v", mt, tail, err)
			}
		}
	}
}

func TestFramePayloadRequiresMap(t *testing.T) {
	for _, mt := range []uint8{MsgTypeStreamHeader, MsgTypeSessionHandshake, MsgTypeDataRecord, MsgTypeCheckpointBarrier, MsgTypeWatermark, MsgTypeEndOfPartition, MsgTypeBackpressure, MsgTypeSessionDrain} {
		for _, payload := range [][]byte{{0xc0}, {0x90}, {0x91, 0xc0}, {0xa0}, {0x00}} {
			if _, err := DecodePayload(Frame{MsgType: mt, Payload: payload}); !errors.Is(err, ErrDecodePayload) {
				t.Fatalf("type %d payload %x: %v", mt, payload, err)
			}
		}
	}
}

func TestNilMessageRejectedBeforeWrite(t *testing.T) {
	for _, msg := range []any{(*StreamHeaderMsg)(nil), (*SessionHandshakeMsg)(nil), (*DataRecordMsg)(nil), (*CheckpointBarrierMsg)(nil), (*WatermarkMsg)(nil), (*EndOfPartitionMsg)(nil), (*BackpressureMsg)(nil), (*SessionDrainMsg)(nil)} {
		var wire bytes.Buffer
		if err := EncodeAndWriteFrame(&wire, msg); !errors.Is(err, ErrEncodePayload) {
			t.Fatalf("%T: %v", msg, err)
		}
		if wire.Len() != 0 {
			t.Fatalf("%T wrote bytes", msg)
		}
	}
}
