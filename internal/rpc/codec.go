package rpc

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/tarungka/wire/internal/protocol"
)

// RPC frame layout constants per TRD Section 3.1.1.
//
// Frame layout:
//
//	Length (uint32, 4B) | MethodID (uint16, 2B) | RequestID (6B, uint48) | Payload (msgpack)
//
// Length = size of MethodID + RequestID + Payload (does NOT include Length field itself).
// No CRC32C — Yamux provides reliable TCP delivery; RPC streams are short-lived.
const (
	RPCLengthFieldSize    = 4
	RPCMethodIDFieldSize  = 2
	RPCRequestIDFieldSize = 6
	RPCHeaderSize         = RPCMethodIDFieldSize + RPCRequestIDFieldSize // 8
	RPCMinFrameLength     = RPCHeaderSize                                // 8 (header only, no payload)
	RPCRequestIDMask      = (1 << 48) - 1                                // lower 48 bits
)

// rpcFramePool reuses frame body buffers to reduce GC pressure.
var rpcFramePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 4096)
		return &b
	},
}

// MethodID identifies an RPC method.
type MethodID uint16

// RPC method identifiers.
const (
	MethodSubmitJob             MethodID = 0x0001
	MethodUpdateTaskStatus      MethodID = 0x0002
	MethodTriggerCheckpoint     MethodID = 0x0003
	MethodAcknowledgeCheckpoint MethodID = 0x0004
	MethodRequestTaskSlots      MethodID = 0x0005
	MethodHeartbeat             MethodID = 0x0006
	MethodRegisterWorker        MethodID = 0x0007
	MethodError                 MethodID = 0x00FF
)

// RPCFrame represents a decoded RPC frame.
type RPCFrame struct {
	MethodID  MethodID
	RequestID uint64
	Payload   []byte
}

// ReadRPCFrame reads a single RPC frame from the reader.
func ReadRPCFrame(r io.Reader, maxPayloadSize int) (RPCFrame, error) {
	// 1. Read length field (4 bytes, big-endian).
	var lenBuf [RPCLengthFieldSize]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return RPCFrame{}, err
	}
	frameLen := binary.BigEndian.Uint32(lenBuf[:])

	// 2. Validate length bounds.
	if frameLen < RPCMinFrameLength {
		return RPCFrame{}, fmt.Errorf("%w: frame length %d below minimum %d", ErrRPCDecodeFailed, frameLen, RPCMinFrameLength)
	}
	payloadLen := int(frameLen) - RPCHeaderSize
	if payloadLen > maxPayloadSize {
		return RPCFrame{}, fmt.Errorf("%w: payload size %d exceeds maximum %d", ErrRPCPayloadTooLarge, payloadLen, maxPayloadSize)
	}

	// 3. Read header (8 bytes) directly.
	var header [RPCHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return RPCFrame{}, err
	}

	// 4. Extract header fields.
	methodID := MethodID(binary.BigEndian.Uint16(header[0:2]))

	// RequestID: 6 bytes, big-endian uint48. Read into a uint64.
	var reqID uint64
	for i := 0; i < RPCRequestIDFieldSize; i++ {
		reqID = (reqID << 8) | uint64(header[2+i])
	}

	// 5. Read payload directly into final slice (no intermediate copy).
	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return RPCFrame{}, err
		}
	}

	return RPCFrame{
		MethodID:  methodID,
		RequestID: reqID,
		Payload:   payload,
	}, nil
}

// WriteRPCFrame writes an RPC frame to the writer.
func WriteRPCFrame(w io.Writer, frame RPCFrame) error {
	payloadLen := len(frame.Payload)
	frameLen := uint32(RPCHeaderSize + payloadLen)
	totalSize := RPCLengthFieldSize + int(frameLen)

	// Get a pooled buffer for the entire write.
	bp := rpcFramePool.Get().(*[]byte)
	buf := *bp
	if cap(buf) < totalSize {
		buf = make([]byte, totalSize)
	} else {
		buf = buf[:totalSize]
	}

	// Build length prefix.
	binary.BigEndian.PutUint32(buf[0:RPCLengthFieldSize], frameLen)

	// Build header: MethodID (2B) + RequestID (6B).
	offset := RPCLengthFieldSize
	binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(frame.MethodID))

	// Encode RequestID as 6 bytes big-endian (lower 48 bits).
	reqID := frame.RequestID & uint64(RPCRequestIDMask)
	for i := RPCRequestIDFieldSize - 1; i >= 0; i-- {
		buf[offset+2+i] = byte(reqID & 0xFF)
		reqID >>= 8
	}

	// Copy payload.
	if payloadLen > 0 {
		copy(buf[offset+RPCHeaderSize:], frame.Payload)
	}

	// Single write call.
	_, err := w.Write(buf)

	// Return buffer to pool.
	*bp = buf
	rpcFramePool.Put(bp)

	return err
}

// EncodeRPCRequest encodes a request message and writes it as an RPC frame.
func EncodeRPCRequest(w io.Writer, methodID MethodID, requestID uint64, msg any) error {
	payload, err := protocol.EncodeMsgPack(msg)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRPCEncodeFailed, err)
	}
	return WriteRPCFrame(w, RPCFrame{
		MethodID:  methodID,
		RequestID: requestID,
		Payload:   payload,
	})
}

// DecodeRPCPayload decodes the payload of an RPC frame into the target struct.
func DecodeRPCPayload(frame RPCFrame, target any) error {
	if err := protocol.DecodeMsgPack(frame.Payload, target); err != nil {
		return fmt.Errorf("%w: %v", ErrRPCDecodeFailed, err)
	}
	return nil
}

// MethodName returns a human-readable name for the given method ID.
func MethodName(id MethodID) string {
	switch id {
	case MethodSubmitJob:
		return "SubmitJob"
	case MethodUpdateTaskStatus:
		return "UpdateTaskStatus"
	case MethodTriggerCheckpoint:
		return "TriggerCheckpoint"
	case MethodAcknowledgeCheckpoint:
		return "AcknowledgeCheckpoint"
	case MethodRequestTaskSlots:
		return "RequestTaskSlots"
	case MethodHeartbeat:
		return "Heartbeat"
	case MethodRegisterWorker:
		return "RegisterWorker"
	case MethodError:
		return "Error"
	default:
		return fmt.Sprintf("Unknown(0x%04X)", uint16(id))
	}
}
