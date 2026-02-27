package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// helper to write and read back a frame
func roundtrip(t *testing.T, msgType uint8, msg any) (Frame, any) {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteFrame(&buf, msgType, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	frame, err := ReadFrame(&buf, DefaultMaxFrameSize)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if frame.MsgType != msgType {
		t.Fatalf("MsgType: got 0x%02X, want 0x%02X", frame.MsgType, msgType)
	}
	decoded, err := DecodePayload(frame)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	return frame, decoded
}

func TestWriteReadFrame_Roundtrip(t *testing.T) {
	tests := []struct {
		name    string
		msgType uint8
		msg     any
		check   func(t *testing.T, decoded any)
	}{
		{
			name:    "Handshake",
			msgType: MsgTypeHandshake,
			msg:     &HandshakeMsg{ProtocolVersion: 1, MinVersion: 1, Features: FeatureCRC32C},
			check: func(t *testing.T, decoded any) {
				m := decoded.(*HandshakeMsg)
				if m.ProtocolVersion != 1 || m.MinVersion != 1 || m.Features != FeatureCRC32C {
					t.Errorf("Handshake mismatch: %+v", m)
				}
			},
		},
		{
			name:    "DataRecord",
			msgType: MsgTypeDataRecord,
			msg: &DataRecordMsg{
				Key:       []byte("key1"),
				Value:     []byte(`{"a":1}`),
				EventTime: 1708819200000,
				Headers:   map[string][]byte{"source": []byte("test")},
			},
			check: func(t *testing.T, decoded any) {
				m := decoded.(*DataRecordMsg)
				if string(m.Key) != "key1" {
					t.Errorf("Key: got %q", m.Key)
				}
				if string(m.Value) != `{"a":1}` {
					t.Errorf("Value: got %q", m.Value)
				}
				if m.EventTime != 1708819200000 {
					t.Errorf("EventTime: got %d", m.EventTime)
				}
				if string(m.Headers["source"]) != "test" {
					t.Errorf("Headers: got %v", m.Headers)
				}
			},
		},
		{
			name:    "CheckpointBarrier",
			msgType: MsgTypeCheckpointBarrier,
			msg:     &CheckpointBarrierMsg{CheckpointID: 42, EpochID: 42, Timestamp: 1708819200000},
			check: func(t *testing.T, decoded any) {
				m := decoded.(*CheckpointBarrierMsg)
				if m.CheckpointID != 42 || m.EpochID != 42 || m.Timestamp != 1708819200000 {
					t.Errorf("CheckpointBarrier mismatch: %+v", m)
				}
			},
		},
		{
			name:    "Watermark",
			msgType: MsgTypeWatermark,
			msg:     &WatermarkMsg{Timestamp: 1708819200000, SourceID: "api-source-0"},
			check: func(t *testing.T, decoded any) {
				m := decoded.(*WatermarkMsg)
				if m.Timestamp != 1708819200000 || m.SourceID != "api-source-0" {
					t.Errorf("Watermark mismatch: %+v", m)
				}
			},
		},
		{
			name:    "EndOfPartition",
			msgType: MsgTypeEndOfPartition,
			msg:     &EndOfPartitionMsg{SourceID: "s-0", Reason: EndReasonExhausted},
			check: func(t *testing.T, decoded any) {
				m := decoded.(*EndOfPartitionMsg)
				if m.SourceID != "s-0" || m.Reason != EndReasonExhausted {
					t.Errorf("EndOfPartition mismatch: %+v", m)
				}
			},
		},
		{
			name:    "Backpressure",
			msgType: MsgTypeBackpressure,
			msg:     &BackpressureMsg{StreamID: 7, State: BackpressurePause, BufferUsage: 0.8},
			check: func(t *testing.T, decoded any) {
				m := decoded.(*BackpressureMsg)
				if m.StreamID != 7 || m.State != BackpressurePause || m.BufferUsage != 0.8 {
					t.Errorf("Backpressure mismatch: %+v", m)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, decoded := roundtrip(t, tt.msgType, tt.msg)
			tt.check(t, decoded)
		})
	}
}

func TestDataRecord_MinimalPayload(t *testing.T) {
	msg := &DataRecordMsg{
		Key:       nil,
		Value:     []byte("v"),
		EventTime: 100,
	}
	_, decoded := roundtrip(t, MsgTypeDataRecord, msg)
	m := decoded.(*DataRecordMsg)
	if m.Key != nil {
		t.Errorf("expected nil Key, got %v", m.Key)
	}
	if m.Headers != nil {
		t.Errorf("expected nil Headers, got %v", m.Headers)
	}
}

func TestDataRecord_MaximumPayload(t *testing.T) {
	// 16 MB value — at the edge of default max frame size.
	bigValue := make([]byte, 15*1024*1024) // Leave room for overhead.
	for i := range bigValue {
		bigValue[i] = byte(i % 256)
	}
	msg := &DataRecordMsg{
		Value:     bigValue,
		EventTime: 1,
	}
	_, decoded := roundtrip(t, MsgTypeDataRecord, msg)
	m := decoded.(*DataRecordMsg)
	if !bytes.Equal(m.Value, bigValue) {
		t.Errorf("large Value roundtrip failed")
	}
}

func TestReadFrame_OversizedRejected(t *testing.T) {
	var buf bytes.Buffer
	// Write a length field that exceeds max frame size.
	binary.Write(&buf, binary.BigEndian, DefaultMaxFrameSize+1)
	// Write dummy data so ReadFull doesn't EOF on length read.
	buf.Write(make([]byte, 100))

	_, err := ReadFrame(&buf, DefaultMaxFrameSize)
	if err != ErrFrameTooLarge {
		t.Errorf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestReadFrame_UnderMinimumRejected(t *testing.T) {
	for _, length := range []uint32{0, 1, 2, 3, 4} {
		t.Run("", func(t *testing.T) {
			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, length)
			buf.Write(make([]byte, 100))

			_, err := ReadFrame(&buf, DefaultMaxFrameSize)
			if err != ErrFrameTooSmall {
				t.Errorf("length=%d: expected ErrFrameTooSmall, got %v", length, err)
			}
		})
	}
}

func TestReadFrame_UnknownMsgType(t *testing.T) {
	// Build a valid frame with unknown MsgType=0xFF.
	payload := []byte{0x80} // Empty msgpack map.
	var buf bytes.Buffer
	if err := WriteFrameRaw(&buf, 0xFF, payload); err != nil {
		t.Fatalf("WriteFrameRaw: %v", err)
	}

	// ReadFrame should succeed (CRC is valid).
	frame, err := ReadFrame(&buf, DefaultMaxFrameSize)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if frame.MsgType != 0xFF {
		t.Errorf("MsgType: got 0x%02X, want 0xFF", frame.MsgType)
	}

	// DecodePayload should return ErrUnknownMsgType.
	_, err = DecodePayload(frame)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("unknown message type")) {
		t.Errorf("expected ErrUnknownMsgType, got %v", err)
	}
}

func TestReadFrame_PartialRead(t *testing.T) {
	// Write a frame, then truncate it.
	var full bytes.Buffer
	msg := &DataRecordMsg{Value: []byte("hello"), EventTime: 1}
	if err := WriteFrame(&full, MsgTypeDataRecord, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	data := full.Bytes()

	// Give only the first half.
	half := bytes.NewReader(data[:len(data)/2])
	_, err := ReadFrame(half, DefaultMaxFrameSize)
	if err == nil {
		t.Fatal("expected error on partial read")
	}
}

func TestReadFrame_PartialLengthField(t *testing.T) {
	// Only 2 bytes of the 4-byte length field.
	r := bytes.NewReader([]byte{0x00, 0x00})
	_, err := ReadFrame(r, DefaultMaxFrameSize)
	if err != io.ErrUnexpectedEOF {
		t.Errorf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestCRC32C_Validation(t *testing.T) {
	msg := &DataRecordMsg{Key: []byte("k"), Value: []byte("v"), EventTime: 100}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, MsgTypeDataRecord, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	data := buf.Bytes()

	// Extract CRC from the frame header.
	crcInFrame := binary.BigEndian.Uint32(data[5:9])

	// Manually compute CRC over MsgType + Payload.
	payload := data[9:]
	crcManual := computeCRC32C(MsgTypeDataRecord, payload)

	if crcInFrame != crcManual {
		t.Errorf("CRC mismatch: frame=0x%08X manual=0x%08X", crcInFrame, crcManual)
	}
}

func TestCRC32C_CorruptionDetected(t *testing.T) {
	msg := &DataRecordMsg{Key: []byte("k"), Value: []byte("v"), EventTime: 100}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, MsgTypeDataRecord, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	data := buf.Bytes()

	// Flip one bit in the payload.
	if len(data) > 9 {
		data[9] ^= 0x01
	}

	_, err := ReadFrame(bytes.NewReader(data), DefaultMaxFrameSize)
	if err != ErrCRCMismatch {
		t.Errorf("expected ErrCRCMismatch, got %v", err)
	}
}

func TestCRC32C_MsgTypeCorrupted(t *testing.T) {
	msg := &DataRecordMsg{Key: []byte("k"), Value: []byte("v"), EventTime: 100}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, MsgTypeDataRecord, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	data := buf.Bytes()

	// Change MsgType byte (offset 4).
	data[4] ^= 0x01

	_, err := ReadFrame(bytes.NewReader(data), DefaultMaxFrameSize)
	if err != ErrCRCMismatch {
		t.Errorf("expected ErrCRCMismatch, got %v", err)
	}
}

func TestWriteFrame_LengthFieldEncoding(t *testing.T) {
	msg := &WatermarkMsg{Timestamp: 100, SourceID: "src"}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, MsgTypeWatermark, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	data := buf.Bytes()

	// Parse length field.
	length := binary.BigEndian.Uint32(data[0:4])
	expectedPayloadLen := len(data) - LengthFieldSize
	if int(length) != expectedPayloadLen {
		t.Errorf("Length field: got %d, want %d (5 + payload)", length, expectedPayloadLen)
	}
	// Verify Length = 5 + payload size.
	payloadSize := len(data) - HeaderSize
	if int(length) != MsgTypeFieldSize+CRCFieldSize+payloadSize {
		t.Errorf("Length should be 5 + %d = %d, got %d", payloadSize, 5+payloadSize, length)
	}
}

func TestDecodePayload_AllTypes(t *testing.T) {
	tests := []struct {
		name    string
		msgType uint8
		msg     any
	}{
		{"Handshake", MsgTypeHandshake, &HandshakeMsg{ProtocolVersion: 1, MinVersion: 1}},
		{"DataRecord", MsgTypeDataRecord, &DataRecordMsg{Value: []byte("v"), EventTime: 1}},
		{"CheckpointBarrier", MsgTypeCheckpointBarrier, &CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1}},
		{"Watermark", MsgTypeWatermark, &WatermarkMsg{Timestamp: 1, SourceID: "s"}},
		{"EndOfPartition", MsgTypeEndOfPartition, &EndOfPartitionMsg{SourceID: "s", Reason: 0}},
		{"Backpressure", MsgTypeBackpressure, &BackpressureMsg{StreamID: 1, State: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := EncodeMsgPack(tt.msg)
			if err != nil {
				t.Fatalf("EncodeMsgPack: %v", err)
			}
			frame := Frame{MsgType: tt.msgType, Payload: payload}
			decoded, err := DecodePayload(frame)
			if err != nil {
				t.Fatalf("DecodePayload: %v", err)
			}
			if decoded == nil {
				t.Fatal("decoded is nil")
			}
		})
	}
}

func TestMultipleFrames_Sequential(t *testing.T) {
	var buf bytes.Buffer

	messages := []struct {
		msgType uint8
		msg     any
	}{
		{MsgTypeHandshake, &HandshakeMsg{ProtocolVersion: 1, MinVersion: 1}},
		{MsgTypeDataRecord, &DataRecordMsg{Value: []byte("v1"), EventTime: 1}},
		{MsgTypeDataRecord, &DataRecordMsg{Value: []byte("v2"), EventTime: 2}},
		{MsgTypeWatermark, &WatermarkMsg{Timestamp: 2, SourceID: "s"}},
		{MsgTypeEndOfPartition, &EndOfPartitionMsg{SourceID: "s", Reason: EndReasonExhausted}},
	}

	for _, m := range messages {
		if err := WriteFrame(&buf, m.msgType, m.msg); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	for i, m := range messages {
		frame, err := ReadFrame(&buf, DefaultMaxFrameSize)
		if err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
		if frame.MsgType != m.msgType {
			t.Errorf("frame[%d]: MsgType got 0x%02X, want 0x%02X", i, frame.MsgType, m.msgType)
		}
		_, err = DecodePayload(frame)
		if err != nil {
			t.Fatalf("DecodePayload[%d]: %v", i, err)
		}
	}

	// No more frames.
	_, err := ReadFrame(&buf, DefaultMaxFrameSize)
	if err != io.EOF {
		t.Errorf("expected EOF after all frames, got %v", err)
	}
}

func TestEmptyPayload(t *testing.T) {
	// A frame with zero-length payload: valid frame, but decoding will fail
	// because msgpack can't decode an empty buffer into a struct.
	var buf bytes.Buffer
	if err := WriteFrameRaw(&buf, MsgTypeDataRecord, nil); err != nil {
		t.Fatalf("WriteFrameRaw: %v", err)
	}

	frame, err := ReadFrame(&buf, DefaultMaxFrameSize)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	_, err = DecodePayload(frame)
	if err == nil {
		t.Error("expected decode error for empty payload")
	}
}

func TestEncodeAndWriteFrame(t *testing.T) {
	messages := []any{
		&HandshakeMsg{ProtocolVersion: 1, MinVersion: 1},
		&DataRecordMsg{Value: []byte("v"), EventTime: 1},
		&CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1},
		&WatermarkMsg{Timestamp: 1, SourceID: "s"},
		&EndOfPartitionMsg{SourceID: "s", Reason: 0},
		&BackpressureMsg{StreamID: 1, State: 0},
		// Non-pointer variants.
		HandshakeMsg{ProtocolVersion: 2, MinVersion: 1},
		DataRecordMsg{Value: []byte("v2"), EventTime: 2},
	}

	for _, msg := range messages {
		var buf bytes.Buffer
		if err := EncodeAndWriteFrame(&buf, msg); err != nil {
			t.Fatalf("EncodeAndWriteFrame(%T): %v", msg, err)
		}
		frame, err := ReadFrame(&buf, DefaultMaxFrameSize)
		if err != nil {
			t.Fatalf("ReadFrame(%T): %v", msg, err)
		}
		_, err = DecodePayload(frame)
		if err != nil {
			t.Fatalf("DecodePayload(%T): %v", msg, err)
		}
	}
}

func TestEncodeAndWriteFrame_UnsupportedType(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeAndWriteFrame(&buf, "not a message")
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestReadFrame_LengthEqualsMax_Accepted(t *testing.T) {
	// Boundary: frameLen == maxFrameSize should be accepted (not rejected as too large).
	// This catches off-by-one in the > vs >= check at frame.go ReadFrame.
	maxFrame := uint32(64)
	payloadSize := int(maxFrame - MinFrameLength)
	payload := bytes.Repeat([]byte{0xAB}, payloadSize)

	var buf bytes.Buffer
	if err := WriteFrameRaw(&buf, MsgTypeDataRecord, payload); err != nil {
		t.Fatalf("WriteFrameRaw: %v", err)
	}

	frame, err := ReadFrame(&buf, maxFrame)
	if err != nil {
		t.Fatalf("ReadFrame with frameLen==max should succeed, got: %v", err)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Errorf("payload mismatch")
	}
}

func TestReadFrame_BufferPoolReuse(t *testing.T) {
	// Ensure the sync.Pool buffer reuse doesn't leak data from a previous
	// larger frame into a subsequent smaller frame.
	largeMsg := &DataRecordMsg{Value: make([]byte, 8192), EventTime: 1}
	var largeBuf bytes.Buffer
	if err := WriteFrame(&largeBuf, MsgTypeDataRecord, largeMsg); err != nil {
		t.Fatalf("WriteFrame(large): %v", err)
	}
	// Read the large frame to return its buffer to the pool.
	if _, err := ReadFrame(&largeBuf, DefaultMaxFrameSize); err != nil {
		t.Fatalf("ReadFrame(large): %v", err)
	}

	// Write and read a small frame — should reuse the pooled buffer.
	smallMsg := &DataRecordMsg{Value: []byte("small"), EventTime: 2}
	var smallBuf bytes.Buffer
	if err := WriteFrame(&smallBuf, MsgTypeDataRecord, smallMsg); err != nil {
		t.Fatalf("WriteFrame(small): %v", err)
	}
	frame, err := ReadFrame(&smallBuf, DefaultMaxFrameSize)
	if err != nil {
		t.Fatalf("ReadFrame(small): %v", err)
	}

	decoded, err := DecodePayload(frame)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	result := decoded.(*DataRecordMsg)
	if string(result.Value) != "small" {
		t.Errorf("payload corrupted by pool reuse: got %q, want %q", result.Value, "small")
	}
}

func TestReadFrame_BodyTruncated_DoesNotPoisonPool(t *testing.T) {
	// If a frame body read fails (truncated), subsequent reads from the pool
	// should still work correctly.

	// 1. Truncated frame: length says MinFrameLength but body shorter.
	bad := []byte{0x00, 0x00, 0x00, 0x05, 0x01} // length=5, only 1 byte of body
	_, err := ReadFrame(bytes.NewReader(bad), DefaultMaxFrameSize)
	if err == nil {
		t.Fatal("expected error on truncated body")
	}

	// 2. A valid frame should still decode correctly after the pool error path.
	var good bytes.Buffer
	if err := WriteFrame(&good, MsgTypeDataRecord, &DataRecordMsg{Value: []byte("ok"), EventTime: 1}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	frame, err := ReadFrame(&good, DefaultMaxFrameSize)
	if err != nil {
		t.Fatalf("ReadFrame after pool error should succeed: %v", err)
	}
	if frame.MsgType != MsgTypeDataRecord {
		t.Errorf("MsgType: got 0x%02X, want 0x%02X", frame.MsgType, MsgTypeDataRecord)
	}
}

func TestWriteFrameRaw_NilPayload_MinFrameLength(t *testing.T) {
	// A nil payload should produce a frame with length == MinFrameLength
	// and total wire size == HeaderSize (no payload bytes on the wire).
	var buf bytes.Buffer
	if err := WriteFrameRaw(&buf, MsgTypeDataRecord, nil); err != nil {
		t.Fatalf("WriteFrameRaw: %v", err)
	}
	data := buf.Bytes()

	gotLen := binary.BigEndian.Uint32(data[0:4])
	if gotLen != MinFrameLength {
		t.Errorf("frame length: got %d, want %d", gotLen, MinFrameLength)
	}
	if len(data) != HeaderSize {
		t.Errorf("wire size: got %d, want %d (header only)", len(data), HeaderSize)
	}
}

func TestDecodePayload_UnknownType_ErrorWrapping(t *testing.T) {
	// Verify that ErrUnknownMsgType is properly wrapped (errors.Is works)
	// and the hex byte is included in the message.
	_, err := DecodePayload(Frame{MsgType: 0xFE, Payload: []byte{0x80}})
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !errors.Is(err, ErrUnknownMsgType) {
		t.Errorf("expected errors.Is(err, ErrUnknownMsgType), got: %v", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("0xFE")) {
		t.Errorf("error should contain hex byte 0xFE, got: %v", err)
	}
}

func TestEncodeAndWriteFrame_AllValueTypes(t *testing.T) {
	// Ensure all non-pointer (value) type variants are correctly mapped
	// in the EncodeAndWriteFrame type switch.
	msgs := []any{
		HandshakeMsg{ProtocolVersion: 1, MinVersion: 1},
		DataRecordMsg{Value: []byte("v"), EventTime: 1},
		CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1},
		WatermarkMsg{Timestamp: 1, SourceID: "s"},
		EndOfPartitionMsg{SourceID: "s", Reason: EndReasonExhausted},
		BackpressureMsg{StreamID: 1, State: BackpressurePause},
	}
	for _, msg := range msgs {
		var buf bytes.Buffer
		if err := EncodeAndWriteFrame(&buf, msg); err != nil {
			t.Fatalf("EncodeAndWriteFrame(%T): %v", msg, err)
		}
		frame, err := ReadFrame(&buf, DefaultMaxFrameSize)
		if err != nil {
			t.Fatalf("ReadFrame(%T): %v", msg, err)
		}
		if _, err := DecodePayload(frame); err != nil {
			t.Fatalf("DecodePayload(%T): %v", msg, err)
		}
	}
}
