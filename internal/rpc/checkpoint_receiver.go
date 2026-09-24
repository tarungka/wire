package rpc

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/hashicorp/yamux"
)

// CheckpointReplicaPublisher must validate assignment/epoch and snapshot
// contents, make referenced artifacts recoverable, and durably publish the
// replica before returning nil. It must honor ctx and must not retain staging.
type CheckpointReplicaPublisher func(ctx context.Context, request ReplicateCheckpointRequest, staging io.Reader) error

// NewCheckpointReplicaHandler creates a bounded receiver for registration with
// Server.RegisterStream. The server owner must authenticate peers. stagingDir
// is an existing private worker directory. Admission never queues transfers.
func NewCheckpointReplicaHandler(stagingDir string, concurrency int, publish CheckpointReplicaPublisher) (StreamHandler, error) {
	if concurrency < 1 || publish == nil {
		return nil, errors.New("replica receiver requires positive concurrency and publisher")
	}
	info, err := os.Stat(stagingDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("replica staging path must be a directory")
	}
	slots := make(chan struct{}, concurrency)
	return func(ctx context.Context, requestID uint64, payload []byte, stream *yamux.Stream) error {
		var request ReplicateCheckpointRequest
		if err := DecodeRPCPayload(RPCFrame{Payload: payload}, &request); err != nil {
			return err
		}
		if err := request.Validate(); err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			return EncodeRPCRequest(stream, MethodReplicateCheckpoint, requestID, CheckpointReplicaAdmission{Accepted: false})
		}
		defer func() { <-slots }()
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		deadline, _ := ctx.Deadline()
		if err := stream.SetDeadline(deadline); err != nil {
			return err
		}
		stop := context.AfterFunc(ctx, func() { _ = stream.SetDeadline(time.Now()); _ = stream.Close() })
		defer stop()
		file, err := os.CreateTemp(stagingDir, ".replica-transfer-")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(file.Name()) }()
		defer file.Close()
		if err := EncodeRPCRequest(stream, MethodReplicateCheckpoint, requestID, CheckpointReplicaAdmission{Accepted: true}); err != nil {
			return err
		}
		if err := ReadCheckpointChunks(ctx, stream, requestID, request.Size, request.SHA256, file); err != nil {
			return err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := publish(ctx, request, file); err != nil {
			return err
		}
		return EncodeRPCRequest(stream, MethodReplicateCheckpoint, requestID, CheckpointReplicaReceipt{Stored: true, Snapshot: request})
	}, nil
}
