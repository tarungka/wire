package rpc

import (
	"encoding/binary"
	"fmt"
	"io"

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

	// 3. Read the frame body (header + payload).
	body := make([]byte, frameLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return RPCFrame{}, err
	}

	// 4. Extract header fields.
	methodID := MethodID(binary.BigEndian.Uint16(body[0:2]))

	// RequestID: 6 bytes, big-endian uint48. Read into a uint64.
	var reqID uint64
	for i := 0; i < RPCRequestIDFieldSize; i++ {
		reqID = (reqID << 8) | uint64(body[2+i])
	}

	// 5. Extract payload.
	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		copy(payload, body[RPCHeaderSize:])
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

	// Build length prefix.
	var lenBuf [RPCLengthFieldSize]byte
	binary.BigEndian.PutUint32(lenBuf[:], frameLen)

	// Build header: MethodID (2B) + RequestID (6B).
	var header [RPCHeaderSize]byte
	binary.BigEndian.PutUint16(header[0:2], uint16(frame.MethodID))

	// Encode RequestID as 6 bytes big-endian (lower 48 bits).
	reqID := frame.RequestID & uint64(RPCRequestIDMask)
	for i := RPCRequestIDFieldSize - 1; i >= 0; i-- {
		header[2+i] = byte(reqID & 0xFF)
		reqID >>= 8
	}

	// Write length prefix.
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}

	// Write header.
	if _, err := w.Write(header[:]); err != nil {
		return err
	}

	// Write payload.
	if payloadLen > 0 {
		if _, err := w.Write(frame.Payload); err != nil {
			return err
		}
	}

	return nil
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
	case MethodError:
		return "Error"
	default:
		return fmt.Sprintf("Unknown(0x%04X)", uint16(id))
	}
}
