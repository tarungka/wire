package protocol

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"sync"
)

// Frame layout constants per WIP-01 Section 3.1.
const (
	LengthFieldSize  = 4
	MsgTypeFieldSize = 1
	CRCFieldSize     = 4
	HeaderSize       = LengthFieldSize + MsgTypeFieldSize + CRCFieldSize // 9
	MinFrameLength   = MsgTypeFieldSize + CRCFieldSize                   // 5
	// DefaultMaxFrameSize is the default maximum frame size (16 MB).
	DefaultMaxFrameSize uint32 = 16 * 1024 * 1024
)

// crc32cTable is a precomputed CRC32C (Castagnoli) table.
// Go automatically selects hardware acceleration (SSE4.2/ARM CRC) when available.
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// The first CRC byte is one of 256 fixed message discriminators. Precompute
// its state once, avoiding a separate checksum call and escaping byte slice
// for every frame. The payload still uses Go's hardware-accelerated Update.
var crc32cTypeSeeds = func() [256]uint32 {
	var seeds [256]uint32
	for i := range seeds {
		seeds[i] = crc32.Update(0, crc32cTable, []byte{byte(i)})
	}
	return seeds
}()

// framePool reuses frame body buffers to reduce GC pressure at high throughput.
var framePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 4096)
		return &b
	},
}

// computeCRC32C computes the CRC32C checksum over MsgType || Payload.
// Uses crc32.Update to avoid hash.Hash32 allocation per call.
func computeCRC32C(msgType byte, payload []byte) uint32 {
	return crc32.Update(crc32cTypeSeeds[msgType], crc32cTable, payload)
}

// Frame represents a decoded wire protocol frame.
type Frame struct {
	// Length is the reported wire length, excluding its four-byte prefix.
	Length uint32
	// Corruption diagnostics are populated only when CRC validation fails.
	ReceivedCRC   uint32
	ComputedCRC   uint32
	CorruptPrefix []byte // At most 64 bytes of the frame body, copied before reuse.
	MsgType       uint8
	Payload       []byte // Raw msgpack bytes, post-CRC verification.
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
		return Frame{Length: frameLen}, ErrFrameTooSmall
	}
	if frameLen > maxFrameSize {
		return Frame{Length: frameLen}, ErrFrameTooLarge
	}

	// 3. Read the frame body using a pooled buffer to reduce GC pressure.
	bufp := framePool.Get().(*[]byte)
	buf := *bufp
	if cap(buf) < int(frameLen) {
		buf = make([]byte, frameLen)
	} else {
		buf = buf[:frameLen]
	}

	if _, err := io.ReadFull(r, buf); err != nil {
		// Return buffer to pool on error.
		if cap(buf) <= 1024*1024 {
			*bufp = buf
			framePool.Put(bufp)
		}
		return Frame{}, err
	}

	// 4. Extract fields.
	msgType := buf[0]
	crcReceived := binary.BigEndian.Uint32(buf[1:5])

	// Verify before allocating the decoded payload. Corrupt frames must not
	// cause a second allocation of the sender-controlled frame length.
	crcComputed := computeCRC32C(msgType, buf[5:])
	if crcReceived != crcComputed {
		diagnostic := Frame{Length: frameLen, MsgType: msgType, ReceivedCRC: crcReceived, ComputedCRC: crcComputed, CorruptPrefix: append([]byte(nil), buf[:min(len(buf), 64)]...)}
		if cap(buf) <= 1024*1024 {
			*bufp = buf
			framePool.Put(bufp)
		}
		return diagnostic, ErrCRCMismatch
	}
	payload := append([]byte(nil), buf[5:]...)
	if cap(buf) <= 1024*1024 {
		*bufp = buf
		framePool.Put(bufp)
	}

	return Frame{Length: frameLen, MsgType: msgType, Payload: payload}, nil
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
	if uint64(len(payload)) > math.MaxUint32-MinFrameLength {
		return ErrFrameTooLarge
	}
	frameLen := uint32(len(payload)) + MinFrameLength

	// Compute CRC32C over MsgType || Payload.
	crc := computeCRC32C(msgType, payload)

	// Build the header: Length (4B) + MsgType (1B) + CRC32C (4B).
	var header [HeaderSize]byte
	binary.BigEndian.PutUint32(header[0:4], frameLen)
	header[4] = msgType
	binary.BigEndian.PutUint32(header[5:9], crc)

	// Write header.
	if n, err := w.Write(header[:]); err != nil {
		return err
	} else if n != len(header) {
		return io.ErrShortWrite
	}

	// Write payload.
	if len(payload) > 0 {
		if n, err := w.Write(payload); err != nil {
			return err
		} else if n != len(payload) {
			return io.ErrShortWrite
		}
	}

	return nil
}

// DecodePayload decodes the raw payload of a Frame into the appropriate message struct.
func DecodePayload(f Frame) (any, error) {
	switch f.MsgType {
	case MsgTypeSessionDrain:
		var msg SessionDrainMsg
		if err := decodeFramePayload(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil
	case MsgTypeStreamHeader:
		var msg StreamHeaderMsg
		if err := decodeFramePayload(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil
	case MsgTypeSessionHandshake:
		var msg SessionHandshakeMsg
		if err := decodeFramePayload(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeDataRecord:
		var msg DataRecordMsg
		if err := decodeFramePayload(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeCheckpointBarrier:
		var msg CheckpointBarrierMsg
		if err := decodeFramePayload(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeWatermark:
		var msg WatermarkMsg
		if err := decodeFramePayload(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeEndOfPartition:
		var msg EndOfPartitionMsg
		if err := decodeFramePayload(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	case MsgTypeBackpressure:
		var msg BackpressureMsg
		if err := decodeFramePayload(f.Payload, &msg); err != nil {
			return nil, err
		}
		return &msg, nil

	default:
		return nil, fmt.Errorf("%w: 0x%02X", ErrUnknownMsgType, f.MsgType)
	}
}

// EncodeAndWriteFrame determines the MsgType from the concrete message type and writes the frame.
func EncodeAndWriteFrame(w io.Writer, msg any) error {
	return EncodeAndWriteFrameLimit(w, msg, DefaultMaxFrameSize)
}

// EncodeAndWriteFrameLimit rejects oversized messages before writing any bytes.
// maxFrameSize counts the type, CRC and payload, excluding the length prefix.
func EncodeAndWriteFrameLimit(w io.Writer, msg any, maxFrameSize uint32) error {
	var msgType uint8
	switch msg.(type) {
	case *SessionDrainMsg, SessionDrainMsg:
		msgType = MsgTypeSessionDrain
	case *StreamHeaderMsg, StreamHeaderMsg:
		msgType = MsgTypeStreamHeader
	case *SessionHandshakeMsg:
		msgType = MsgTypeSessionHandshake
	case SessionHandshakeMsg:
		msgType = MsgTypeSessionHandshake
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
	if maxFrameSize < MinFrameLength {
		return ErrFrameTooLarge
	}
	encoded, err := encodeMsgPackPrefix(msg, maxFrameSize-MinFrameLength, HeaderSize)
	if err != nil {
		return err
	}
	payload := encoded[HeaderSize:]
	if len(payload) == 1 && payload[0] == 0xc0 {
		return fmt.Errorf("%w: nil message", ErrEncodePayload)
	}
	binary.BigEndian.PutUint32(encoded[:4], uint32(len(payload))+MinFrameLength)
	encoded[4] = msgType
	binary.BigEndian.PutUint32(encoded[5:9], computeCRC32C(msgType, payload))
	if n, err := w.Write(encoded); err != nil {
		return err
	} else if n != len(encoded) {
		return io.ErrShortWrite
	}
	return nil
}
