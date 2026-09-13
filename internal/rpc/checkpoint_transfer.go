package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

const CheckpointChunkSize = 1024 * 1024

// WriteCheckpointChunks streams exactly size bytes without buffering the whole
// snapshot. Cancellation of a blocked transport write is owned by the caller.
func WriteCheckpointChunks(ctx context.Context, w io.Writer, requestID uint64, r io.Reader, size uint64) error {
	return writeCheckpointChunks(ctx, w, MethodReplicateCheckpoint, requestID, r, size)
}

func writeCheckpointChunks(ctx context.Context, w io.Writer, method MethodID, requestID uint64, r io.Reader, size uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	buffer := make([]byte, 8+CheckpointChunkSize)
	for offset := uint64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(uint64(CheckpointChunkSize), size-offset)
		binary.BigEndian.PutUint64(buffer[:8], offset)
		if _, err := io.ReadFull(r, buffer[8:8+int(n)]); err != nil {
			return err
		}
		if err := WriteRPCFrame(w, RPCFrame{MethodID: method, RequestID: requestID, Payload: buffer[:8+int(n)]}); err != nil {
			return err
		}
		offset += n
	}
	return nil
}

// ReadCheckpointChunks validates identity, offsets, length and checksum while
// writing into private staging storage. The caller must discard staging on any
// error, and fsync/publish it before sending a success acknowledgement. size
// must already have been checked against the receiver's storage quota.
func ReadCheckpointChunks(ctx context.Context, r io.Reader, requestID uint64, size uint64, digest [sha256.Size]byte, staging io.Writer) error {
	return readCheckpointChunks(ctx, r, MethodReplicateCheckpoint, requestID, size, digest, staging)
}

func readCheckpointChunks(ctx context.Context, r io.Reader, method MethodID, requestID uint64, size uint64, digest [sha256.Size]byte, staging io.Writer) error {
	hash := sha256.New()
	for offset := uint64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := ReadRPCFrame(r, CheckpointChunkSize+8)
		if err != nil {
			return err
		}
		if frame.MethodID != method || frame.RequestID != requestID {
			return errors.New("checkpoint chunk identity mismatch")
		}
		if len(frame.Payload) <= 8 {
			return errors.New("empty checkpoint chunk")
		}
		if binary.BigEndian.Uint64(frame.Payload[:8]) != offset {
			return errors.New("checkpoint chunk offset mismatch")
		}
		data := frame.Payload[8:]
		if uint64(len(data)) > size-offset {
			return errors.New("checkpoint chunk exceeds declared size")
		}
		if n, err := staging.Write(data); err != nil {
			return err
		} else if n != len(data) {
			return io.ErrShortWrite
		}
		_, _ = hash.Write(data)
		offset += uint64(len(data))
	}
	if got := hash.Sum(nil); string(got) != string(digest[:]) {
		return errors.New("checkpoint transfer checksum mismatch")
	}
	return ctx.Err()
}
