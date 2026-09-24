package protocol

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestCanonicalEmptyBinaryDoesNotMutateRecord(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		record := DataRecordMsg{Headers: map[string][]byte{"empty": nil}}
		var msg any = record
		if pointer {
			msg = &record
		}
		payload, err := EncodeMsgPack(msg)
		if err != nil {
			t.Fatal(err)
		}
		if record.Value != nil || record.Headers["empty"] != nil {
			t.Fatal("encoding mutated caller values")
		}
		if !bytes.Contains(payload, []byte{0xa1, 'v', 0xc4, 0}) || !bytes.Contains(payload, []byte{0xa5, 'e', 'm', 'p', 't', 'y', 0xc4, 0}) {
			t.Fatalf("empty values are not binary: %x", payload)
		}
		var wire bytes.Buffer
		if err := EncodeAndWriteFrame(&wire, msg); err != nil {
			t.Fatal(err)
		}
		frame, err := ReadFrame(&wire, DefaultMaxFrameSize)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodePayload(frame); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPayloadFieldTypes(t *testing.T) {
	cases := []struct {
		kind   uint8
		fields map[string]any
		key    string
		bad    []any
	}{
		{MsgTypeDataRecord, map[string]any{"v": []byte{}, "t": 0}, "v", []any{nil, "legacy raw", true, 1}},
		{MsgTypeDataRecord, map[string]any{"v": []byte{}, "t": 0}, "t", []any{nil, "0", float64(1), true}},
		{MsgTypeDataRecord, map[string]any{"v": []byte{}, "t": 0}, "k", []any{"text", 1}},
		{MsgTypeDataRecord, map[string]any{"v": []byte{}, "t": 0}, "h", []any{nil, []any{}, map[string]any{"x": nil}, map[string]any{"x": "text"}}},
		{MsgTypeStreamHeader, map[string]any{"src": "s", "dst": "d"}, "src", []any{nil, []byte("s"), 1}},
		{MsgTypeStreamHeader, map[string]any{"src": "s", "dst": "d"}, "p", []any{-1, 65536, nil, "0"}},
		{MsgTypeSessionHandshake, map[string]any{"v": 1, "min_v": 1, "f": 0, "n": "n"}, "v", []any{nil, -1, 65536, "1"}},
		{MsgTypeCheckpointBarrier, map[string]any{"c": 1, "e": 1, "ts": 0}, "c", []any{nil, -1, "1"}},
		{MsgTypeWatermark, map[string]any{"t": 0, "s": "s"}, "s", []any{nil, []byte("s"), true}},
		{MsgTypeEndOfPartition, map[string]any{"s": "s", "r": 0}, "r", []any{nil, -1, 3, 256, "0"}},
		{MsgTypeBackpressure, map[string]any{"id": 1, "st": 0}, "bu", []any{nil, "0", 1, true, float32(-0.1), float32(1.1), float32(math.NaN()), float32(math.Inf(1))}},
		{MsgTypeSessionDrain, map[string]any{"r": false}, "r", []any{nil, 0, "false"}},
	}
	for _, tc := range cases {
		for _, bad := range tc.bad {
			tc.fields[tc.key] = bad
			payload, err := EncodeMsgPack(tc.fields)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodePayload(Frame{MsgType: tc.kind, Payload: payload}); !errors.Is(err, ErrDecodePayload) {
				t.Fatalf("type %d field %s value %#v: %v", tc.kind, tc.key, bad, err)
			}
		}
	}
}
