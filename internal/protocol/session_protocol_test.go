package protocol

import (
	"bytes"
	"reflect"
	"testing"
)

func TestSessionProtocolMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    uint8
		message any
	}{
		{"StreamHeader", 0x00, &StreamHeaderMsg{SourceTaskID: "source-0", TargetTaskID: "map-1", PartitionIndex: 2}},
		{"StreamHeader", 0x00, &StreamHeaderMsg{SourceTaskID: "source-0", TargetTaskID: "map-1"}},
		{"SessionHandshake", 0x07, &SessionHandshakeMsg{ProtocolVersion: 2, MinVersion: 1, Features: FeatureCRC32C, NodeID: "worker-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := EncodeAndWriteFrame(&b, tc.message); err != nil {
				t.Fatal(err)
			}
			f, err := ReadFrame(&b, DefaultMaxFrameSize)
			if err != nil {
				t.Fatal(err)
			}
			if f.MsgType != tc.kind || MsgTypeName(f.MsgType) != tc.name {
				t.Fatalf("message identity: %d %s", f.MsgType, MsgTypeName(f.MsgType))
			}
			got, err := DecodePayload(f)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.message) {
				t.Fatalf("got %+v want %+v", got, tc.message)
			}
		})
	}
}

func TestMinimalRecordOmitsOptionalFields(t *testing.T) {
	payload, err := EncodeMsgPack(&DataRecordMsg{Value: []byte("v")})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := DecodeMsgPack(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["k"]; ok {
		t.Fatal("nil key encoded")
	}
	if _, ok := fields["h"]; ok {
		t.Fatal("empty headers encoded")
	}
	if _, ok := fields["v"]; !ok {
		t.Fatal("missing required value")
	}
	if _, ok := fields["t"]; !ok {
		t.Fatal("zero timestamp omitted")
	}
}

func TestMaximumLegalRecordFrame(t *testing.T) {
	// At this size the msgpack bin length takes five bytes. Determine metadata
	// overhead with a value using the same length representation.
	record := &DataRecordMsg{Value: make([]byte, 65536)}
	payload, err := EncodeMsgPack(record)
	if err != nil {
		t.Fatal(err)
	}
	overhead := len(payload) - len(record.Value)
	record.Value = make([]byte, int(DefaultMaxFrameSize)-MinFrameLength-overhead)
	var wire bytes.Buffer
	if err := EncodeAndWriteFrame(&wire, record); err != nil {
		t.Fatal(err)
	}
	if wire.Len() != int(DefaultMaxFrameSize)+LengthFieldSize {
		t.Fatalf("size %d", wire.Len())
	}
	frame, err := ReadFrame(&wire, DefaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePayload(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.(*DataRecordMsg).Value) != len(record.Value) {
		t.Fatal("maximum record truncated")
	}
}

func TestCorruptFrameDiagnosticBounds(t *testing.T) {
	var wire bytes.Buffer
	if err := EncodeAndWriteFrame(&wire, &DataRecordMsg{Value: make([]byte, 4096)}); err != nil {
		t.Fatal(err)
	}
	data := wire.Bytes()
	data[len(data)-1] ^= 1
	frame, err := ReadFrame(bytes.NewReader(data), DefaultMaxFrameSize)
	if err != ErrCRCMismatch {
		t.Fatalf("expected corruption, got %v", err)
	}
	if frame.Payload != nil {
		t.Fatal("corrupt payload allocated or exposed as decoded content")
	}
	if len(frame.CorruptPrefix) != 64 || !bytes.Equal(frame.CorruptPrefix, data[4:68]) {
		t.Fatal("diagnostic must contain exactly first 64 body bytes")
	}
	if frame.ReceivedCRC == frame.ComputedCRC {
		t.Fatal("diagnostic CRCs incorrectly match")
	}
	if int(frame.Length)+LengthFieldSize != len(data) {
		t.Fatalf("reported length %d", frame.Length)
	}
	saved := append([]byte(nil), frame.CorruptPrefix...)
	for i := 0; i < 20; i++ {
		_, _ = ReadFrame(bytes.NewReader(data), DefaultMaxFrameSize)
	}
	if !bytes.Equal(saved, frame.CorruptPrefix) {
		t.Fatal("diagnostic aliases reused buffer")
	}
}
