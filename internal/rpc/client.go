package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/protocol"
)

// Client opens Yamux streams to send RPC requests and read responses.
type Client struct {
	session   *yamux.Session
	openGate  chan struct{}
	openOnce  sync.Once
	cfg       Config
	nextReqID atomic.Uint64
	log       zerolog.Logger
}

// NewClient creates a new RPC client using the given Yamux session.
func NewClient(session *yamux.Session, cfg Config) *Client {
	return &Client{
		session: session,

		cfg: cfg,
		log: logger.GetLogger("rpc-client"),
	}
}

// nextRequestID returns a monotonically increasing request ID masked to 48 bits.
func (c *Client) nextRequestID() uint64 {
	return c.nextReqID.Add(1) & uint64(RPCRequestIDMask)
}

// Call performs a single RPC call. It opens a new Yamux stream, sends the request,
// reads the response, and closes the stream. The response is decoded into the
// provided response pointer. If the server returns an RPCError, it is returned.
func (c *Client) Call(ctx context.Context, method MethodID, request any, response any) (callErr error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.methodTimeout(method))
		defer cancel()
	}
	defer func() {
		if callErr != nil && ctx.Err() != nil {
			callErr = errors.Join(callErr, ctx.Err())
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				callErr = errors.Join(callErr, ErrRPCTimeout)
			}
		}
	}()
	stream, err := c.openStreamContext(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRPCClosed, err)
	}
	defer func() { _ = stream.Close() }()

	// Set deadline from context or method-specific default.
	deadline, ok := ctx.Deadline()
	if !ok {
		timeout := c.cfg.methodTimeout(method)
		deadline = time.Now().Add(timeout)
	}
	if err := stream.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	stopClose := context.AfterFunc(ctx, func() { _ = stream.SetDeadline(time.Now()); _ = stream.Close() })
	defer stopClose()

	reqID := c.nextRequestID()

	c.log.Debug().
		Str("method", MethodName(method)).
		Uint64("request_id", reqID).
		Msg("sending RPC request")

	// Encode and write request.
	if err := EncodeRPCRequest(stream, method, reqID, request); err != nil {
		return err
	}

	// Read response frame.
	respFrame, err := ReadRPCFrame(stream, c.cfg.MaxPayloadSize)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("%w: %v", ErrRPCTimeout, err)
		}
		return fmt.Errorf("%w: %w", ErrRPCClosed, err)
	}

	if respFrame.RequestID != reqID || (respFrame.MethodID != method && respFrame.MethodID != MethodError) {
		return fmt.Errorf("%w: response does not match request", ErrRPCDecodeFailed)
	}

	// Check for error response.
	if respFrame.MethodID == MethodError {
		var rpcErr RPCError
		if decErr := protocol.DecodeMsgPack(respFrame.Payload, &rpcErr); decErr != nil {
			return fmt.Errorf("%w: failed to decode error response: %v", ErrRPCDecodeFailed, decErr)
		}
		return &rpcErr
	}

	// Decode success response.
	if err := DecodeRPCPayload(respFrame, response); err != nil {
		return err
	}

	return nil
}

// CallStream opens a server-streaming RPC. It opens a fresh yamux stream,
// writes the request frame, and returns a channel that yields each
// response frame as the server pushes it. Closing the returned cancel
// function tears down the stream.
//
// The channel is closed when:
//   - the server closes the stream (clean EOF)
//   - the underlying yamux session shuts down
//   - the caller invokes cancel()
//
// Errors received during reading are surfaced via StreamFrame.Err and
// terminate the channel. The caller MUST drain or call cancel().
func (c *Client) CallStream(ctx context.Context, method MethodID, request any) (<-chan StreamFrame, func(), error) {
	stream, err := c.openStreamContext(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrRPCClosed, err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stopClose := context.AfterFunc(streamCtx, func() { _ = stream.SetDeadline(time.Now()); _ = stream.Close() })
	cleanup := func() { cancel(); stopClose(); _ = stream.SetDeadline(time.Now()); _ = stream.Close() }
	reqID := c.nextRequestID()
	c.log.Debug().
		Str("method", MethodName(method)).
		Uint64("request_id", reqID).
		Msg("opening stream RPC")

	if err := EncodeRPCRequest(stream, method, reqID, request); err != nil {
		cleanup()
		return nil, nil, err
	}

	out := make(chan StreamFrame, 16)

	go func() {
		defer cleanup()
		defer close(out)
		for {
			frame, readErr := ReadRPCFrame(stream, c.cfg.MaxPayloadSize)
			if readErr != nil {
				if streamCtx.Err() == nil {
					select {
					case out <- StreamFrame{Err: readErr}:
					case <-streamCtx.Done():
					}
				}
				return
			}
			if frame.RequestID != reqID || (frame.MethodID != method && frame.MethodID != MethodError) {
				select {
				case out <- StreamFrame{Err: fmt.Errorf("%w: response does not match request", ErrRPCDecodeFailed)}:
				case <-streamCtx.Done():
				}
				return
			}
			if frame.MethodID == MethodError {
				var rpcErr RPCError
				if decErr := protocol.DecodeMsgPack(frame.Payload, &rpcErr); decErr != nil {
					select {
					case out <- StreamFrame{Err: fmt.Errorf("%w: %v", ErrRPCDecodeFailed, decErr)}:
					case <-streamCtx.Done():
					}
					return
				}
				select {
				case out <- StreamFrame{Err: &rpcErr}:
				case <-streamCtx.Done():
				}
				return
			}
			select {
			case out <- StreamFrame{Frame: frame}:
			case <-streamCtx.Done():
				return
			}
		}
	}()

	return out, cleanup, nil
}

// StreamFrame is one element from a server-streaming RPC. Either Frame is
// populated with a successful payload or Err is set with the read/decode
// failure that ended the stream.
type StreamFrame struct {
	Frame RPCFrame
	Err   error
}

// CallWithRetry wraps Call with exponential backoff retry logic.
// Only retryable errors are retried.
func (c *Client) CallWithRetry(ctx context.Context, method MethodID, request any, response any, maxRetries int) error {
	var lastErr error
	maxRetries = max(0, maxRetries)

	for attempt := 0; attempt <= max(0, maxRetries); attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = c.Call(ctx, method, request, response)
		if lastErr == nil {
			return nil
		}

		// Check if error is retryable.
		var rpcErr *RPCError
		retryable := errors.As(lastErr, &rpcErr) && rpcErr.Retryable
		var networkError net.Error
		if safeToRetry(method, request) && (errors.Is(lastErr, ErrRPCTimeout) || errors.Is(lastErr, ErrRPCClosed) || errors.Is(lastErr, io.EOF) || (errors.As(lastErr, &networkError) && networkError.Timeout())) {
			retryable = true
		}
		if !retryable {
			return lastErr
		}

		// Don't retry on last attempt.
		if attempt == maxRetries {
			break
		}

		// Exponential backoff: 100ms * 2^attempt, capped at 5s.
		backoff := time.Duration(math.Min(
			float64(100*time.Millisecond)*math.Pow(2, float64(attempt)),
			float64(5*time.Second),
		))

		// Use RetryAfterMs from server if provided.
		if rpcErr != nil && rpcErr.RetryAfterMs > 0 {
			backoff = time.Duration(min(rpcErr.RetryAfterMs, 5000)) * time.Millisecond
		}

		// Add jitter: ±25% of computed backoff.
		jitter := time.Duration(rand.Int64N(int64(backoff)/2)) - backoff/4
		backoff += jitter

		c.log.Debug().
			Str("method", MethodName(method)).
			Int("attempt", attempt+1).
			Dur("backoff", backoff).
			Msg("retrying RPC call")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	return lastErr
}

// SubmitJob sends a SubmitJob RPC.
func (c *Client) SubmitJob(ctx context.Context, req *SubmitJobRequest) (*SubmitJobResponse, error) {
	var resp SubmitJobResponse
	if err := c.Call(ctx, MethodSubmitJob, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateTaskStatus sends an UpdateTaskStatus RPC.
func (c *Client) UpdateTaskStatus(ctx context.Context, req *UpdateTaskStatusRequest) (*UpdateTaskStatusResponse, error) {
	var resp UpdateTaskStatusResponse
	if err := c.CallWithRetry(ctx, MethodUpdateTaskStatus, req, &resp, c.cfg.MaxRetries); err != nil {
		return nil, err
	}
	return &resp, nil
}

// TriggerCheckpoint sends a TriggerCheckpoint RPC.
func (c *Client) TriggerCheckpoint(ctx context.Context, req *TriggerCheckpointRequest) (*TriggerCheckpointResponse, error) {
	var resp TriggerCheckpointResponse
	if err := c.Call(ctx, MethodTriggerCheckpoint, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AcknowledgeCheckpoint sends an AcknowledgeCheckpoint RPC.
func (c *Client) AcknowledgeCheckpoint(ctx context.Context, req *AcknowledgeCheckpointRequest) (*AcknowledgeCheckpointResponse, error) {
	var resp AcknowledgeCheckpointResponse
	if err := c.CallWithRetry(ctx, MethodAcknowledgeCheckpoint, req, &resp, c.cfg.MaxRetries); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RequestTaskSlots sends a RequestTaskSlots RPC.
func (c *Client) RequestTaskSlots(ctx context.Context, req *RequestTaskSlotsRequest) (*RequestTaskSlotsResponse, error) {
	var resp RequestTaskSlotsResponse
	if err := c.Call(ctx, MethodRequestTaskSlots, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Heartbeat sends a Heartbeat RPC.
func (c *Client) Heartbeat(ctx context.Context, req *HeartbeatRequest) (*HeartbeatResponse, error) {
	var resp HeartbeatResponse
	if err := c.Call(ctx, MethodHeartbeat, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RegisterWorker sends a RegisterWorker RPC.
func (c *Client) RegisterWorker(ctx context.Context, req *RegisterWorkerRequest) (*RegisterWorkerResponse, error) {
	var resp RegisterWorkerResponse
	if err := c.Call(ctx, MethodRegisterWorker, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Only requests carrying stable operation identities can retry ambiguous
// transport failures. Registration is intentionally excluded: it reconciles
// worker state and must not be replayed on a different session implicitly.
func safeToRetry(method MethodID, request any) bool {
	switch method {
	case MethodUpdateTaskStatus, MethodAcknowledgeCheckpoint, MethodTriggerCheckpoint, MethodHeartbeat:
		return true
	case MethodSubmitJob:
		req, ok := request.(*SubmitJobRequest)
		return ok && req.AttemptID != "" && req.ReservationID != ""
	case MethodRequestTaskSlots:
		req, ok := request.(*RequestTaskSlotsRequest)
		return ok && (req.RequiredSlots == 0 || req.ReservationID != "")
	}
	return false
}
