package rpc

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/hashicorp/yamux"
)

// FetchCheckpointRequest names a stored snapshot and the deployment requesting
// recovery. EpochID belongs to the snapshot; DeploymentEpoch fences its reader.
type FetchCheckpointRequest struct {
	WorkerID        string `codec:"wid"`
	DeploymentEpoch uint64 `codec:"deid"`
	JobID           string `codec:"jid"`
	TaskID          string `codec:"tid"`
	CheckpointID    uint64 `codec:"cid"`
	EpochID         uint64 `codec:"eid"`
}

func (r FetchCheckpointRequest) Validate() error {
	if r.WorkerID == "" || len(r.WorkerID) > 4096 || r.DeploymentEpoch == 0 {
		return errors.New("invalid checkpoint recovery deployment")
	}
	return (ReplicateCheckpointRequest{JobID: r.JobID, TaskID: r.TaskID, CheckpointID: r.CheckpointID, EpochID: r.EpochID, Size: 1}).Validate()
}

func (r FetchCheckpointRequest) matches(snapshot ReplicateCheckpointRequest) bool {
	return snapshot.JobID == r.JobID && snapshot.TaskID == r.TaskID && snapshot.CheckpointID == r.CheckpointID && snapshot.EpochID == r.EpochID && snapshot.Format == CheckpointFormatArchive
}

// FetchCheckpoint writes a verified archive into private staging storage. The
// caller must discard staging on any error and validate/import the archive
// before allowing a task to process records.
func (c *Client) FetchCheckpoint(ctx context.Context, request FetchCheckpointRequest, staging io.Writer) error {
	if err := request.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	stream, err := c.openStreamContext(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	deadline, _ := ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = stream.SetDeadline(time.Now()); _ = stream.Close() })
	defer stop()
	id := c.nextRequestID()
	if err := EncodeRPCRequest(stream, MethodFetchCheckpoint, id, request); err != nil {
		return err
	}
	frame, err := ReadRPCFrame(stream, 32*1024)
	if err != nil {
		return err
	}
	if frame.RequestID != id {
		return errors.New("checkpoint fetch request mismatch")
	}
	if frame.MethodID == MethodError {
		var remote RPCError
		if err := DecodeRPCPayload(frame, &remote); err != nil {
			return err
		}
		return &remote
	}
	if frame.MethodID != MethodFetchCheckpoint {
		return errors.New("checkpoint fetch method mismatch")
	}
	var snapshot ReplicateCheckpointRequest
	if err := DecodeRPCPayload(frame, &snapshot); err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if !request.matches(snapshot) {
		return errors.New("checkpoint fetch snapshot mismatch")
	}
	return readCheckpointChunks(ctx, stream, MethodFetchCheckpoint, id, snapshot.Size, snapshot.SHA256, staging)
}

// CheckpointArchiveLoader must authorize the deployment against a completed
// checkpoint before opening its archive. The returned body is closed by the
// handler on every path. Loading and body reads must honor ctx.
type CheckpointArchiveLoader func(context.Context, FetchCheckpointRequest) (ReplicateCheckpointRequest, io.ReadCloser, error)

// NewCheckpointFetchHandler bounds concurrent recovery exports without queuing.
func NewCheckpointFetchHandler(concurrency int, load CheckpointArchiveLoader) (StreamHandler, error) {
	if concurrency < 1 || load == nil {
		return nil, errors.New("checkpoint fetch requires positive concurrency and loader")
	}
	slots := make(chan struct{}, concurrency)
	return func(ctx context.Context, id uint64, payload []byte, stream *yamux.Stream) error {
		var request FetchCheckpointRequest
		if err := DecodeRPCPayload(RPCFrame{Payload: payload}, &request); err != nil {
			return err
		}
		if err := request.Validate(); err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			return errors.New("checkpoint recovery capacity exhausted")
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
		// The request has no further inbound frames. Observe the caller's
		// half-close so a cancelled fetch also interrupts archive loading or
		// a write blocked on the peer's receive window.
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			var unexpected [1]byte
			_, _ = stream.Read(unexpected[:])
			cancel()
		}()
		defer func() {
			cancel()
			_ = stream.SetReadDeadline(time.Now())
			<-readerDone
		}()
		snapshot, body, err := load(ctx, request)
		if body != nil {
			defer body.Close()
		}
		if err != nil {
			return err
		}
		if body == nil {
			return errors.New("checkpoint archive loader returned no body")
		}
		if err := snapshot.Validate(); err != nil {
			return err
		}
		if !request.matches(snapshot) {
			return errors.New("checkpoint archive loader identity mismatch")
		}
		if err := EncodeRPCRequest(stream, MethodFetchCheckpoint, id, snapshot); err != nil {
			return err
		}
		return writeCheckpointChunks(ctx, stream, MethodFetchCheckpoint, id, body, snapshot.Size)
	}, nil
}

// AuthorizeCheckpointFetchRequest binds the serving replica to the deployment requesting state.
type AuthorizeCheckpointFetchRequest struct {
	ReplicaWorkerID string                 `codec:"rwid"`
	Fetch           FetchCheckpointRequest `codec:"fetch"`
}
