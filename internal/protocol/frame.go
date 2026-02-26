package protocol

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

// Frame layout constants per WIP-01 Section 3.1.
const (
	LengthFieldSize  = 4
	MsgTypeFieldSize = 1
	CRCFieldSize     = 4
	HeaderSize       = LengthFieldSize + MsgTypeFieldSize + CRCFieldSize // 9
	MinFrameLength   = MsgTypeFieldSize + CRCFieldSize                  // 5
	// DefaultMaxFrameSize is the default maximum frame size (16 MB).
	DefaultMaxFrameSize uint32 = 16 * 1024 * 1024
)

// crc32cTable is a precomputed CRC32C (Castagnoli) table.
// Go automatically selects hardware acceleration (SSE4.2/ARM CRC) when available.
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// computeCRC32C computes the CRC32C checksum over MsgType || Payload.
func computeCRC32C(msgType byte, payload []byte) uint32 {
	h := crc32.New(crc32cTable)
	h.Write([]byte{msgType})
	h.Write(payload)
	return h.Sum32()
}

// Frame represents a decoded wire protocol frame.
type Frame struct {
	MsgType uint8
	Payload []byte // Raw msgpack bytes, post-CRC verification.
}

// ReadFrame reads a single frame from the reader.
// It validates the length bounds and CRC32C checksum.
func ReadFrame(r io.Reader, maxFrameSize uint32) (Frame, error) {
	// 1. Read length field (4 bytes, big-endian).
	var lenBuf [LengthFieldSize]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return Frame{}, err
	}
	frameLen := binary.BigEndian.Uint32(lenBuf[:])

	// 2. Validate length.
	if frameLen < MinFrameLength {
		return Frame{}, ErrFrameTooSmall
	}
	if frameLen > maxFrameSize {
		return Frame{}, ErrFrameTooLarge
	}

	// 3. Read the frame body.
	buf := make([]byte, frameLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Frame{}, err
	}

	// 4. Extract fields.
	msgType := buf[0]
	crcReceived := binary.BigEndian.Uint32(buf[1:5])
	payload := buf[5:]

	// 5. Verify CRC32C over MsgType || Payload.
	crcComputed := computeCRC32C(msgType, payload)
	if crcReceived != crcComputed {
		return Frame{}, ErrCRCMismatch
	}

	return Frame{MsgType: msgType, Payload: payload}, nil
}

// WriteFrame encodes a message and writes a complete frame to the writer.
func WriteFrame(w io.Writer, msgType uint8, msg any) error {
	payload, err := EncodeMsgPack(msg)
	if err != nil {
		return err
	}
	return WriteFrameRaw(w, msgType, payload)
}

// WriteFrameRaw writes a frame with a pre-encoded payload.
func WriteFrameRaw(w io.Writer, msgType uint8, payload []byte) error {
	frameLen := uint32(MsgTypeFieldSize + CRCFieldSize + len(payload))

	// Compute CRC32C over MsgType || Payload.
	crc := computeCRC32C(msgType, payload)

	// Build the header: Length (4B) + MsgType (1B) + CRC32C (4B).
	var header [HeaderSize]byte
	binary.BigEndian.PutUint32(header[0:4], frameLen)
	header[4] = msgType
	binary.BigEndian.PutUint32(header[5:9], crc)

	// Write header.
	if _, err := w.Write(header[:]); err != nil {
		return err
	}

	// Write payload.
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}

	return nil
}

// DecodePayload decodes the raw payload of a Frame into the appropriate message struct.
func DecodePayload(f Frame) (any, error) {
	switch f.MsgType {
	case MsgTypeHandshake:
		var msg HandshakeMsg
		if err := DecodeMsgPack(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeDataRecord:
		var msg DataRecordMsg
		if err := DecodeMsgPack(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeCheckpointBarrier:
		var msg CheckpointBarrierMsg
		if err := DecodeMsgPack(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeWatermark:
		var msg WatermarkMsg
		if err := DecodeMsgPack(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeEndOfPartition:
		var msg EndOfPartitionMsg
		if err := DecodeMsgPack(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeBackpressure:
		var msg BackpressureMsg
		if err := DecodeMsgPack(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	default:
		return nil, fmt.Errorf("%w: 0x%02X", ErrUnknownMsgType, f.MsgType)
	}
}

// EncodeAndWriteFrame determines the MsgType from the concrete message type and writes the frame.
func EncodeAndWriteFrame(w io.Writer, msg any) error {
	var msgType uint8
	switch msg.(type) {
	case *HandshakeMsg:
		msgType = MsgTypeHandshake
	case HandshakeMsg:
		msgType = MsgTypeHandshake
	case *DataRecordMsg:
		msgType = MsgTypeDataRecord
	case DataRecordMsg:
		msgType = MsgTypeDataRecord
	case *CheckpointBarrierMsg:
		msgType = MsgTypeCheckpointBarrier
	case CheckpointBarrierMsg:
		msgType = MsgTypeCheckpointBarrier
	case *WatermarkMsg:
		msgType = MsgTypeWatermark
	case WatermarkMsg:
		msgType = MsgTypeWatermark
	case *EndOfPartitionMsg:
		msgType = MsgTypeEndOfPartition
	case EndOfPartitionMsg:
		msgType = MsgTypeEndOfPartition
	case *BackpressureMsg:
		msgType = MsgTypeBackpressure
	case BackpressureMsg:
		msgType = MsgTypeBackpressure
	default:
		return fmt.Errorf("%w: unsupported type %T", ErrEncodePayload, msg)
	}
	return WriteFrame(w, msgType, msg)
}
