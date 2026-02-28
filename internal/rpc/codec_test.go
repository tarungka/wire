package rpc

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestRPCFrameRoundtrip(t *testing.T) {
	methods := []MethodID{
		MethodSubmitJob, MethodUpdateTaskStatus, MethodTriggerCheckpoint,
		MethodAcknowledgeCheckpoint, MethodRequestTaskSlots, MethodHeartbeat,
		MethodError,
	}

	for _, method := range methods {
		t.Run(MethodName(method), func(t *testing.T) {
			payload := []byte("test payload for " + MethodName(method))
			original := RPCFrame{
				MethodID:  method,
				RequestID: 42,
				Payload:   payload,
			}

			var buf bytes.Buffer
			if err := WriteRPCFrame(&buf, original); err != nil {
				t.Fatalf("WriteRPCFrame: %v", err)
			}

			decoded, err := ReadRPCFrame(&buf, MaxRPCPayloadSize)
			if err != nil {
				t.Fatalf("ReadRPCFrame: %v", err)
			}

			if decoded.MethodID != original.MethodID {
				t.Errorf("MethodID = %d, want %d", decoded.MethodID, original.MethodID)
			}
			if decoded.RequestID != original.RequestID {
				t.Errorf("RequestID = %d, want %d", decoded.RequestID, original.RequestID)
			}
			if !bytes.Equal(decoded.Payload, original.Payload) {
				t.Errorf("Payload mismatch")
			}
		})
	}
}

func TestRPCFrameUint48Boundary(t *testing.T) {
	maxUint48 := uint64((1 << 48) - 1)

	frame := RPCFrame{
		MethodID:  MethodHeartbeat,
		RequestID: maxUint48,
		Payload:   []byte("hi"),
	}

	var buf bytes.Buffer
	if err := WriteRPCFrame(&buf, frame); err != nil {
		t.Fatalf("WriteRPCFrame: %v", err)
	}

	decoded, err := ReadRPCFrame(&buf, MaxRPCPayloadSize)
	if err != nil {
		t.Fatalf("ReadRPCFrame: %v", err)
	}

	if decoded.RequestID != maxUint48 {
		t.Errorf("RequestID = %d, want %d (max uint48)", decoded.RequestID, maxUint48)
	}
}

func TestRPCFrameUint48Truncation(t *testing.T) {
	// Request ID with bits above 48 should be masked.
	fullID := uint64(1<<48) + 123
	frame := RPCFrame{
		MethodID:  MethodHeartbeat,
		RequestID: fullID,
		Payload:   []byte("hi"),
	}

	var buf bytes.Buffer
	if err := WriteRPCFrame(&buf, frame); err != nil {
		t.Fatalf("WriteRPCFrame: %v", err)
	}

	decoded, err := ReadRPCFrame(&buf, MaxRPCPayloadSize)
	if err != nil {
		t.Fatalf("ReadRPCFrame: %v", err)
	}

	expected := fullID & uint64(RPCRequestIDMask)
	if decoded.RequestID != expected {
		t.Errorf("RequestID = %d, want %d (masked)", decoded.RequestID, expected)
	}
}

func TestRPCFrameEmptyPayload(t *testing.T) {
	frame := RPCFrame{
		MethodID:  MethodHeartbeat,
		RequestID: 1,
		Payload:   nil,
	}

	var buf bytes.Buffer
	if err := WriteRPCFrame(&buf, frame); err != nil {
		t.Fatalf("WriteRPCFrame: %v", err)
	}

	decoded, err := ReadRPCFrame(&buf, MaxRPCPayloadSize)
	if err != nil {
		t.Fatalf("ReadRPCFrame: %v", err)
	}

	if len(decoded.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(decoded.Payload))
	}
}

func TestRPCFrameTooSmall(t *testing.T) {
	// Write a length field indicating a frame smaller than the minimum.
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(4)) // less than RPCMinFrameLength (8)

	_, err := ReadRPCFrame(&buf, MaxRPCPayloadSize)
	if err == nil {
		t.Fatal("expected error for frame too small")
	}
}

func TestRPCFramePayloadTooLarge(t *testing.T) {
	// Write a length field indicating a payload that exceeds max.
	maxPayload := 100
	var buf bytes.Buffer
	frameLen := uint32(RPCHeaderSize + maxPayload + 1)
	_ = binary.Write(&buf, binary.BigEndian, frameLen)
	// Write enough dummy data.
	buf.Write(make([]byte, frameLen))

	_, err := ReadRPCFrame(&buf, maxPayload)
	if err == nil {
		t.Fatal("expected error for payload too large")
	}
}

func TestRPCFrameTruncatedRead(t *testing.T) {
	// Write a valid length field but truncate the body.
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(RPCHeaderSize+10))
	buf.Write(make([]byte, 5)) // Only 5 bytes instead of RPCHeaderSize+10.

	_, err := ReadRPCFrame(&buf, MaxRPCPayloadSize)
	if err == nil || err == io.EOF {
		// We expect an error (io.ErrUnexpectedEOF from io.ReadFull).
		if err == nil {
			t.Fatal("expected error for truncated read")
		}
	}
}

func TestMethodName(t *testing.T) {
	tests := []struct {
		id   MethodID
		want string
	}{
		{MethodSubmitJob, "SubmitJob"},
		{MethodUpdateTaskStatus, "UpdateTaskStatus"},
		{MethodTriggerCheckpoint, "TriggerCheckpoint"},
		{MethodAcknowledgeCheckpoint, "AcknowledgeCheckpoint"},
		{MethodRequestTaskSlots, "RequestTaskSlots"},
		{MethodHeartbeat, "Heartbeat"},
		{MethodError, "Error"},
		{MethodID(0xBEEF), "Unknown(0xBEEF)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := MethodName(tt.id)
			if got != tt.want {
				t.Errorf("MethodName(0x%04X) = %q, want %q", uint16(tt.id), got, tt.want)
			}
		})
	}
}

func TestEncodeDecodeRPCRequest(t *testing.T) {
	type testMsg struct {
		Name  string `codec:"n"`
		Value int    `codec:"v"`
	}

	original := testMsg{Name: "hello", Value: 42}

	var buf bytes.Buffer
	if err := EncodeRPCRequest(&buf, MethodSubmitJob, 99, &original); err != nil {
		t.Fatalf("EncodeRPCRequest: %v", err)
	}

	frame, err := ReadRPCFrame(&buf, MaxRPCPayloadSize)
	if err != nil {
		t.Fatalf("ReadRPCFrame: %v", err)
	}

	if frame.MethodID != MethodSubmitJob {
		t.Errorf("MethodID = %d, want %d", frame.MethodID, MethodSubmitJob)
	}
	if frame.RequestID != 99 {
		t.Errorf("RequestID = %d, want 99", frame.RequestID)
	}

	var decoded testMsg
	if err := DecodeRPCPayload(frame, &decoded); err != nil {
		t.Fatalf("DecodeRPCPayload: %v", err)
	}

	if decoded.Name != original.Name || decoded.Value != original.Value {
		t.Errorf("decoded = %+v, want %+v", decoded, original)
	}
}
