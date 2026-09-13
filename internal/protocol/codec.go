package protocol

import (
	"bytes"
	"fmt"

	"github.com/hashicorp/go-msgpack/v2/codec"
)

// msgpackHandle is the shared msgpack handle used for all encode/decode operations.
var msgpackHandle codec.MsgpackHandle

// payloadBuffer bounds retained encoded bytes before a frame reaches the wire.
// A rejected write is atomic, including large binary values handed to Write.
type payloadBuffer struct {
	buffer   bytes.Buffer
	limit    uint32
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
	buf := payloadBuffer{limit: limit}
	err := codec.NewEncoder(&buf, &msgpackHandle).Encode(v)
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
	if err := enc.Encode(v); err != nil {
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
	return nil
}
