package protocol

import (
	"bytes"
	"testing"
)

func FuzzReadFrame(f *testing.F) {
	// Seed with valid encoded frames for all 6 active message types.
	seeds := []struct {
		msgType uint8
		msg     any
	}{
		{MsgTypeHandshake, &HandshakeMsg{ProtocolVersion: 1, MinVersion: 1, Features: FeatureCRC32C}},
		{MsgTypeDataRecord, &DataRecordMsg{Key: []byte("k"), Value: []byte("v"), EventTime: 100}},
		{MsgTypeCheckpointBarrier, &CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1000}},
		{MsgTypeWatermark, &WatermarkMsg{Timestamp: 500, SourceID: "src-0"}},
		{MsgTypeEndOfPartition, &EndOfPartitionMsg{SourceID: "s-0", Reason: EndReasonExhausted}},
		{MsgTypeBackpressure, &BackpressureMsg{StreamID: 1, State: BackpressurePause, BufferUsage: 0.5}},
	}

	for _, s := range seeds {
		var buf bytes.Buffer
		if err := WriteFrame(&buf, s.msgType, s.msg); err != nil {
			f.Fatalf("seed encode: %v", err)
		}
		f.Add(buf.Bytes())
	}

	// Edge case seeds.
	f.Add([]byte{})                       // Empty input.
	f.Add([]byte{0x00})                   // 1 byte.
	f.Add([]byte{0x00, 0x00, 0x00, 0x05}) // Min length, no body.
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // Max length field.

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		// Must never panic. Bounded allocation (max 16 MB).
		frame, err := ReadFrame(r, DefaultMaxFrameSize)
		if err != nil {
			return
		}
		// If ReadFrame succeeded, DecodePayload must not panic.
		_, _ = DecodePayload(frame)
	})
}

func FuzzDecodePayload(f *testing.F) {
	// Seed with valid payloads for all 6 active message types.
	seeds := []struct {
		msgType uint8
		msg     any
	}{
		{MsgTypeHandshake, &HandshakeMsg{ProtocolVersion: 1, MinVersion: 1}},
		{MsgTypeDataRecord, &DataRecordMsg{Value: []byte("v"), EventTime: 1}},
		{MsgTypeCheckpointBarrier, &CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1}},
		{MsgTypeWatermark, &WatermarkMsg{Timestamp: 1, SourceID: "s"}},
		{MsgTypeEndOfPartition, &EndOfPartitionMsg{SourceID: "s", Reason: 0}},
		{MsgTypeBackpressure, &BackpressureMsg{StreamID: 1, State: 0}},
	}

	for _, s := range seeds {
		payload, err := EncodeMsgPack(s.msg)
		if err != nil {
			f.Fatalf("seed encode: %v", err)
		}
		f.Add(s.msgType, payload)
	}

	// Edge cases.
	f.Add(uint8(0xFF), []byte{0x80})       // Unknown type, valid msgpack.
	f.Add(MsgTypeDataRecord, []byte{})     // Empty payload.
	f.Add(MsgTypeDataRecord, []byte{0xC1}) // Invalid msgpack.

	f.Fuzz(func(t *testing.T, msgType uint8, payload []byte) {
		frame := Frame{MsgType: msgType, Payload: payload}
		// Must never panic.
		_, _ = DecodePayload(frame)
	})
}
