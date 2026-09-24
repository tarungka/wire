package protocol

import (
	"bytes"
	"testing"
)

func TestDataRecordUsesMessagePackBinary(t *testing.T) {
	msg := &DataRecordMsg{Key: []byte{0xff, 0x00}, Value: []byte{0x00, 0xfe, 0x80}, Headers: map[string][]byte{"x": {0xff}}}
	payload, err := EncodeMsgPack(msg)
	if err != nil {
		t.Fatal(err)
	}
	for _, sequence := range [][]byte{
		{0xa1, 'k', 0xc4, 2, 0xff, 0x00},
		{0xa1, 'v', 0xc4, 3, 0x00, 0xfe, 0x80},
		{0xa1, 'x', 0xc4, 1, 0xff},
	} {
		if !bytes.Contains(payload, sequence) {
			t.Fatalf("missing bin encoding %x in %x", sequence, payload)
		}
	}
	var wire bytes.Buffer
	if err := EncodeAndWriteFrame(&wire, msg); err != nil {
		t.Fatal(err)
	}
	frame, err := ReadFrame(&wire, DefaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Fatal("frame writer changed the binary representation")
	}
}
