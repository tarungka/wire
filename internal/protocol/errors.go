package protocol

import "errors"

var (
	// ErrFrameTooLarge is returned when the frame length exceeds the configured maximum.
	ErrFrameTooLarge = errors.New("protocol: frame length exceeds maximum")

	// ErrFrameTooSmall is returned when the frame length is below the minimum (5 bytes).
	ErrFrameTooSmall = errors.New("protocol: frame length below minimum (5)")

	// ErrCRCMismatch is returned when the CRC32C checksum does not match.
	ErrCRCMismatch = errors.New("protocol: CRC32C checksum mismatch")

	// ErrUnknownMsgType is returned when the message type is not recognized.
	ErrUnknownMsgType = errors.New("protocol: unknown message type")

	// ErrDecodePayload is returned when msgpack decoding of the payload fails.
	ErrDecodePayload = errors.New("protocol: failed to decode payload")

	// ErrEncodePayload is returned when msgpack encoding of the payload fails.
	ErrEncodePayload = errors.New("protocol: failed to encode payload")

	// ErrHandshakeTimeout is returned when the handshake is not received within the timeout.
	ErrHandshakeTimeout = errors.New("protocol: handshake timeout")

	// ErrHandshakeExpected is returned when the first frame on a stream is not a Handshake.
	ErrHandshakeExpected = errors.New("protocol: expected handshake as first frame")

	// ErrVersionIncompatible is returned when protocol versions are incompatible.
	ErrVersionIncompatible = errors.New("protocol: incompatible protocol version")
)
