package rpc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

const MaxCheckpointTransferSize = 64 * 1024 * 1024

// ReplicateCheckpointRequest precedes the chunk frames on a dedicated stream.
// The body is an encoded task snapshot; referenced state files require separate
// replication before a receiver may declare the task snapshot recoverable.
type ReplicateCheckpointRequest struct {
	JobID        string            `codec:"jid"`
	TaskID       string            `codec:"tid"`
	CheckpointID uint64            `codec:"cid"`
	EpochID      uint64            `codec:"eid"`
	Size         uint64            `codec:"size"`
	SHA256       [sha256.Size]byte `codec:"sha256"`
}

func (r ReplicateCheckpointRequest) Validate() error {
	if r.JobID == "" || r.TaskID == "" || len(r.JobID) > 4096 || len(r.TaskID) > 4096 || r.CheckpointID == 0 {
		return errors.New("invalid checkpoint replica identity")
	}
	if r.Size == 0 || r.Size > MaxCheckpointTransferSize {
		return errors.New("invalid checkpoint replica size")
	}
	return nil
}

// CheckpointReplicaReceipt is sent only after durable publication. Echoing the
// full identity, length and checksum binds the acknowledgement to this transfer.
type CheckpointReplicaReceipt struct {
	Stored   bool                       `codec:"stored"`
	Snapshot ReplicateCheckpointRequest `codec:"snapshot"`
}

// CheckpointReplicaAdmission grants capacity before the sender writes chunks.
type CheckpointReplicaAdmission struct {
	Accepted bool `codec:"accepted"`
}

func (c *Client) ReplicateCheckpoint(ctx context.Context, request ReplicateCheckpointRequest, body io.Reader) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
	}
	stream, err := c.openStreamContext(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	deadline, _ := ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = stream.SetDeadline(time.Now()); _ = stream.Close() })
	defer stop()
	requestID := c.nextRequestID()
	if err := EncodeRPCRequest(stream, MethodReplicateCheckpoint, requestID, request); err != nil {
		return err
	}
	admissionFrame, err := ReadRPCFrame(stream, 1024)
	if err != nil {
		return err
	}
	if admissionFrame.MethodID != MethodReplicateCheckpoint || admissionFrame.RequestID != requestID {
		return errors.New("checkpoint admission identity mismatch")
	}
	var admission CheckpointReplicaAdmission
	if err := DecodeRPCPayload(admissionFrame, &admission); err != nil {
		return err
	}
	if !admission.Accepted {
		return errors.New("checkpoint replica admission rejected")
	}
	if err := WriteCheckpointChunks(ctx, stream, requestID, body, request.Size); err != nil {
		return err
	}
	frame, err := ReadRPCFrame(stream, 32*1024)
	if err != nil {
		return err
	}
	if frame.RequestID != requestID {
		return errors.New("checkpoint receipt request mismatch")
	}
	if frame.MethodID == MethodError {
		var rpcErr RPCError
		if err := protocol.DecodeMsgPack(frame.Payload, &rpcErr); err != nil {
			return err
		}
		return &rpcErr
	}
	if frame.MethodID != MethodReplicateCheckpoint {
		return errors.New("checkpoint receipt method mismatch")
	}
	var receipt CheckpointReplicaReceipt
	if err := DecodeRPCPayload(frame, &receipt); err != nil {
		return err
	}
	if !receipt.Stored || receipt.Snapshot != request {
		return fmt.Errorf("checkpoint receipt does not confirm requested replica")
	}
	return nil
}
