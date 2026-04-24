package rpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
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
	cfg       Config
	nextReqID atomic.Uint64
	log       zerolog.Logger
}

// NewClient creates a new RPC client using the given Yamux session.
func NewClient(session *yamux.Session, cfg Config) *Client {
	return &Client{
		session: session,
		cfg:     cfg,
		log:     logger.GetLogger("rpc-client"),
	}
}

// nextRequestID returns a monotonically increasing request ID masked to 48 bits.
func (c *Client) nextRequestID() uint64 {
	return c.nextReqID.Add(1) & uint64(RPCRequestIDMask)
}

// Call performs a single RPC call. It opens a new Yamux stream, sends the request,
// reads the response, and closes the stream. The response is decoded into the
// provided response pointer. If the server returns an RPCError, it is returned.
func (c *Client) Call(ctx context.Context, method MethodID, request any, response any) error {
	stream, err := c.session.OpenStream()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRPCClosed, err)
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
		return fmt.Errorf("%w: %v", ErrRPCClosed, err)
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

// CallWithRetry wraps Call with exponential backoff retry logic.
// Only retryable errors are retried.
func (c *Client) CallWithRetry(ctx context.Context, method MethodID, request any, response any, maxRetries int) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = c.Call(ctx, method, request, response)
		if lastErr == nil {
			return nil
		}

		// Check if error is retryable.
		rpcErr, ok := lastErr.(*RPCError)
		if !ok || !rpcErr.Retryable {
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
		if rpcErr.RetryAfterMs > 0 {
			backoff = time.Duration(rpcErr.RetryAfterMs) * time.Millisecond
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
	if err := c.Call(ctx, MethodUpdateTaskStatus, req, &resp); err != nil {
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
	if err := c.Call(ctx, MethodAcknowledgeCheckpoint, req, &resp); err != nil {
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
