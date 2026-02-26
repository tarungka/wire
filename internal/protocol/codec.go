package protocol

import (
	"bytes"
	"fmt"

	"github.com/hashicorp/go-msgpack/v2/codec"
)

// msgpackHandle is the shared msgpack handle used for all encode/decode operations.
var msgpackHandle codec.MsgpackHandle

// EncodeMsgPack encodes a value to msgpack bytes.
func EncodeMsgPack(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := codec.NewEncoder(&buf, &msgpackHandle)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncodePayload, err)
	}
	return buf.Bytes(), nil
}

// DecodeMsgPack decodes msgpack bytes into the provided value.
func DecodeMsgPack(data []byte, v interface{}) error {
	dec := codec.NewDecoderBytes(data, &msgpackHandle)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrDecodePayload, err)
	}
	return nil
}
