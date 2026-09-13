package protocol

import (
	"bytes"
	"fmt"
	"math"

	"github.com/hashicorp/go-msgpack/v2/codec"
)

// msgpackHandle is the shared msgpack handle used for all encode/decode operations.
// WriteExt selects the current MessagePack spec, including bin8/bin16/bin32
// for byte slices instead of the legacy raw-string representation.
var msgpackHandle = codec.MsgpackHandle{WriteExt: true}

// payloadBuffer bounds retained encoded bytes before a frame reaches the wire.
// A rejected write is atomic, including large binary values handed to Write.
type payloadBuffer struct {
	buffer   bytes.Buffer
	limit    uint64
	exceeded bool
}

func (b *payloadBuffer) Write(p []byte) (int, error) {
	if uint64(b.buffer.Len())+uint64(len(p)) > uint64(b.limit) {
		b.exceeded = true
		return 0, ErrFrameTooLarge
	}
	return b.buffer.Write(p)
}

func encodeMsgPackLimit(v any, limit uint32) ([]byte, error) {
	return encodeMsgPackPrefix(v, limit, 0)
}

// encodeMsgPackPrefix reserves header space in the same allocation as the payload.
func encodeMsgPackPrefix(v any, limit uint32, prefix int) ([]byte, error) {
	buf := payloadBuffer{limit: uint64(limit) + uint64(prefix)}
	if prefix != 0 {
		buf.buffer.Write(make([]byte, prefix))
	}
	err := codec.NewEncoder(&buf, &msgpackHandle).Encode(canonicalMessage(v))
	if buf.exceeded {
		return nil, ErrFrameTooLarge
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncodePayload, err)
	}
	return buf.buffer.Bytes(), nil
}

// EncodeMsgPack encodes a value to msgpack bytes.
func EncodeMsgPack(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := codec.NewEncoder(&buf, &msgpackHandle)
	if err := enc.Encode(canonicalMessage(v)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncodePayload, err)
	}
	return buf.Bytes(), nil
}

// DecodeMsgPack decodes msgpack bytes into the provided value.
func DecodeMsgPack(data []byte, v any) error {
	dec := codec.NewDecoderBytes(data, &msgpackHandle)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrDecodePayload, err)
	}
	return nil
}

// decodeFramePayload requires exactly one MessagePack object per frame.
// RPC callers of DecodeMsgPack retain their existing decoding contract.
func decodeFramePayload(data []byte, v any) error {
	var required []string
	switch v.(type) {
	case *StreamHeaderMsg:
		required = []string{"src", "dst"}
	case *SessionHandshakeMsg:
		required = []string{"v", "min_v", "f", "n"}
	case *DataRecordMsg:
		required = []string{"v", "t"}
	case *CheckpointBarrierMsg:
		required = []string{"c", "e", "ts"}
	case *WatermarkMsg:
		required = []string{"t", "s"}
	case *EndOfPartitionMsg:
		required = []string{"s", "r"}
	case *BackpressureMsg:
		required = []string{"id", "st"}
	case *SessionDrainMsg:
		required = []string{"r"}
	}
	if !hasRequiredFields(data, required, v) {
		return fmt.Errorf("%w: malformed map or missing/duplicate required field", ErrDecodePayload)
	}
	// Every active Wire message is a map. The generic codec also accepts nil
	// and positional struct arrays, which are not this protocol's schema.
	if len(data) == 0 || ((data[0] < 0x80 || data[0] > 0x8f) && data[0] != 0xde && data[0] != 0xdf) {
		return fmt.Errorf("%w: message must be a map", ErrDecodePayload)
	}
	dec := codec.NewDecoderBytes(data, &msgpackHandle)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrDecodePayload, err)
	}
	if dec.NumBytesRead() != len(data) {
		return fmt.Errorf("%w: trailing bytes after message", ErrDecodePayload)
	}
	switch message := v.(type) {
	case *EndOfPartitionMsg:
		if message.Reason > EndReasonError {
			return fmt.Errorf("%w: invalid partition end reason", ErrDecodePayload)
		}
	case *BackpressureMsg:
		if message.State > BackpressurePause || math.IsNaN(float64(message.BufferUsage)) || message.BufferUsage < 0 || message.BufferUsage > 1 {
			return fmt.Errorf("%w: invalid backpressure value", ErrDecodePayload)
		}
	}
	return nil
}
