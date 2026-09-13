package protocol

import (
	"bytes"
	"encoding/hex"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSpecificationExamples(t *testing.T) {
	cases := []struct {
		name string
		msg  any
		wire string
	}{
		{"DataRecord", &DataRecordMsg{Key: []byte("usr1"), Value: []byte("{\"a\":1}\n"), EventTime: 1708819200000}, "000000250117400a6983a16bc40475737231a174d30000018ddd8fb800a176c4087b2261223a317d0a"},
		{"CheckpointBarrier", &CheckpointBarrierMsg{CheckpointID: 42, EpochID: 42, Timestamp: 1708819200000}, "00000018021672ac9c83a1632aa1652aa27473d30000018ddd8fb800"},
		{"StreamHeader", &StreamHeaderMsg{SourceTaskID: "map-op-3", TargetTaskID: "reduce-op-1", PartitionIndex: 2}, "00000026007e67949783a3647374ab7265647563652d6f702d31a17002a3737263a86d61702d6f702d33"},
		{"EndOfPartition", &EndOfPartitionMsg{SourceID: "s-0", Reason: 0}, "0000000f04e5a4014882a17200a173a3732d30"},
	}
	document, err := os.ReadFile("../../docs/trds/WIP-01/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		var wire bytes.Buffer
		if err := EncodeAndWriteFrame(&wire, tc.msg); err != nil {
			t.Fatal(err)
		}
		actual := hex.EncodeToString(wire.Bytes())
		if actual != tc.wire {
			t.Fatalf("%s: got %s, want %s", tc.name, actual, tc.wire)
		}
		if !strings.Contains(string(document), tc.wire) {
			t.Fatalf("%s example missing from specification", tc.name)
		}
		frame, err := ReadFrame(&wire, DefaultMaxFrameSize)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodePayload(frame)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decoded, tc.msg) {
			t.Fatalf("%s does not decode to documented values", tc.name)
		}
	}
}
