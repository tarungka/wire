package protocol

import (
	"errors"
	"testing"
)

func TestRequiredPayloadFields(t *testing.T) {
	cases := []struct {
		kind   uint8
		fields map[string]any
	}{
		{MsgTypeStreamHeader, map[string]any{"src": "s", "dst": "d"}},
		{MsgTypeSessionHandshake, map[string]any{"v": 1, "min_v": 1, "f": 0, "n": "node"}},
		{MsgTypeDataRecord, map[string]any{"v": []byte("value"), "t": 0}},
		{MsgTypeCheckpointBarrier, map[string]any{"c": 1, "e": 1, "ts": 0}},
		{MsgTypeWatermark, map[string]any{"t": 0, "s": "s"}},
		{MsgTypeEndOfPartition, map[string]any{"s": "s", "r": 0}},
		{MsgTypeBackpressure, map[string]any{"id": 1, "st": 0}},
		{MsgTypeSessionDrain, map[string]any{"r": false}},
	}
	for _, tc := range cases {
		encode := func(fields map[string]any) []byte {
			p, err := EncodeMsgPack(fields)
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
		keys := make([]string, 0, len(tc.fields))
		for key := range tc.fields {
			keys = append(keys, key)
		}
		for _, key := range keys {
			value := tc.fields[key]
			delete(tc.fields, key)
			if _, err := DecodePayload(Frame{MsgType: tc.kind, Payload: encode(tc.fields)}); !errors.Is(err, ErrDecodePayload) {
				t.Fatalf("type %d missing %q: %v", tc.kind, key, err)
			}
			tc.fields[key] = value
		}
		// Forward extensions may contain nested values; they must not hide fields
		// or alter the decoding of valid zero-valued required fields.
		tc.fields["future"] = []any{map[string]any{"nested": []byte{0xff}}, true, nil}
		if _, err := DecodePayload(Frame{MsgType: tc.kind, Payload: encode(tc.fields)}); err != nil {
			t.Fatalf("type %d extension: %v", tc.kind, err)
		}
	}
	// Duplicate required fields are ambiguous even when both values are legal.
	duplicate := []byte{0x82, 0xa1, 'r', 0xc2, 0xa1, 'r', 0xc3}
	if _, err := DecodePayload(Frame{MsgType: MsgTypeSessionDrain, Payload: duplicate}); !errors.Is(err, ErrDecodePayload) {
		t.Fatal("duplicate field accepted")
	}
}
