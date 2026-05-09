package rpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/observability"
	"github.com/tarungka/wire/internal/protocol"
)

// recordRPCServerOp records duration + count + error metrics for a single
// RPC handler dispatch. Method label is bounded to the MethodName enum.
func recordRPCServerOp(method string, start time.Time, err error) {
	hist, count, errs := observability.RPCInstruments()
	attrs := metric.WithAttributes(attribute.String("method", method))
	hist.Record(context.Background(), time.Since(start).Seconds(), attrs)
	count.Add(context.Background(), 1, attrs)
	if err != nil {
		errs.Add(context.Background(), 1, attrs)
	}
}

// Handler is the function signature for RPC method handlers.
// It receives the raw payload bytes; the handler decodes into its specific request type.
// It returns a response struct on success, or an *RPCError on failure.
type Handler func(ctx context.Context, requestID uint64, payload []byte) (any, *RPCError)

// Server accepts Yamux streams and dispatches RPCs to registered handlers.
type Server struct {
	cfg      Config
	handlers map[MethodID]Handler
	log      zerolog.Logger
	mu       sync.RWMutex
	wg       sync.WaitGroup
	cancel   context.CancelFunc
	session  *yamux.Session
}

// NewServer creates a new RPC server with the given configuration.
func NewServer(cfg Config) *Server {
	return &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      logger.GetLogger("rpc-server"),
	}
}

// Register registers a handler for the given method ID.
func (s *Server) Register(method MethodID, handler Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = handler
}

// getHandler returns the handler for the given method ID, or nil if not registered.
func (s *Server) getHandler(method MethodID) Handler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handlers[method]
}

// ServeSession accepts streams from the Yamux session and dispatches each to a goroutine.
// It blocks until the context is canceled or the session is closed.
func (s *Server) ServeSession(ctx context.Context, session *yamux.Session) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.session = session
	defer cancel()

	sem := make(chan struct{}, s.cfg.MaxConcurrentRPCs)

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			// Session closed or context canceled.
			select {
			case <-ctx.Done():
				return
			default:
			}
			s.log.Debug().Err(err).Msg("accept stream ended")
			return
		}

		sem <- struct{}{}
		s.wg.Add(1)
		go func(stream *yamux.Stream) {
			defer func() { <-sem }()
			defer s.wg.Done()
			s.serveStream(ctx, stream)
		}(stream)
	}
}

// serveStream handles a single RPC stream lifecycle.
func (s *Server) serveStream(ctx context.Context, stream *yamux.Stream) {
	defer func() { _ = stream.Close() }()

	defer func() {
		if r := recover(); r != nil {
			s.log.Error().Interface("panic", r).Msg("handler panic recovered")
			rpcErr := NewRPCError(ErrCodeInternalError, fmt.Sprintf("handler panic: %v", r))
			s.writeErrorResponse(stream, 0, rpcErr)
		}
	}()

	// 1. Read the request frame.
	frame, err := ReadRPCFrame(stream, s.cfg.MaxPayloadSize)
	if err != nil {
		s.log.Debug().Err(err).Msg("failed to read RPC frame")
		return
	}

	s.log.Debug().
		Str("method", MethodName(frame.MethodID)).
		Uint64("request_id", frame.RequestID).
		Msg("received RPC request")

	// Time the handler dispatch + response write so the metric reflects
	// the full server-side cost a worker observes via Client.Call.
	dispatchStart := time.Now()
	method := MethodName(frame.MethodID)
	var dispatchErr error
	defer func() {
		recordRPCServerOp(method, dispatchStart, dispatchErr)
	}()

	// 2. Look up handler.
	handler := s.getHandler(frame.MethodID)
	if handler == nil {
		rpcErr := NewRPCError(ErrCodeUnknownMethod,
			fmt.Sprintf("no handler registered for method %s", MethodName(frame.MethodID)))
		s.writeErrorResponse(stream, frame.RequestID, rpcErr)
		dispatchErr = rpcErr
		return
	}

	// 3. Call handler.
	result, rpcErr := handler(ctx, frame.RequestID, frame.Payload)

	// 4. Write response.
	if rpcErr != nil {
		s.writeErrorResponse(stream, frame.RequestID, rpcErr)
		dispatchErr = rpcErr
		return
	}

	s.writeSuccessResponse(stream, frame.MethodID, frame.RequestID, result)
}

// writeErrorResponse writes an error response frame.
func (s *Server) writeErrorResponse(w net.Conn, requestID uint64, rpcErr *RPCError) {
	payload, err := protocol.EncodeMsgPack(rpcErr)
	if err != nil {
		s.log.Error().Err(err).Msg("failed to encode error response")
		return
	}

	writeErr := WriteRPCFrame(w, RPCFrame{
		MethodID:  MethodError,
		RequestID: requestID,
		Payload:   payload,
	})
	if writeErr != nil {
		s.log.Debug().Err(writeErr).Msg("failed to write error response")
	}
}

// writeSuccessResponse writes a success response frame.
func (s *Server) writeSuccessResponse(w net.Conn, method MethodID, requestID uint64, result any) {
	payload, err := protocol.EncodeMsgPack(result)
	if err != nil {
		s.log.Error().Err(err).Msg("failed to encode success response")
		rpcErr := NewRPCError(ErrCodeSerializationError, "failed to encode response")
		s.writeErrorResponse(w, requestID, rpcErr)
		return
	}

	writeErr := WriteRPCFrame(w, RPCFrame{
		MethodID:  method,
		RequestID: requestID,
		Payload:   payload,
	})
	if writeErr != nil {
		s.log.Debug().Err(writeErr).Msg("failed to write success response")
	}
}

// Stop cancels the server context, closes the session to unblock AcceptStream,
// and waits for all in-flight RPCs to complete.
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.session != nil {
		_ = s.session.Close()
	}
	s.wg.Wait()
}
