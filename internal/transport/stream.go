package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/protocol"
)

// NegotiatedParams holds the result of a handshake negotiation.
type NegotiatedParams struct {
	EffectiveVersion uint16
	Features         uint32
}

// FrameStream provides high-level stream operations with handshake,
// error counting, and protocol validation on top of a Yamux stream.
type FrameStream struct {
	mu                   sync.Mutex
	writeMu              sync.Mutex
	readMu               sync.Mutex
	reportMu             sync.Mutex
	reportedPause        bool
	closeOnce            sync.Once
	done                 chan struct{}
	resume               chan struct{}
	session              *Session
	sender               bool
	header               *protocol.StreamHeaderMsg
	raw                  *yamux.Stream
	cfg                  Config
	negotiated           *NegotiatedParams
	consecutiveCRCErrors int
	consecutiveDecErrors int
	lastWatermarks       map[string]int64 // per-SourceID watermark tracking
	ended                bool
	log                  zerolog.Logger
}

// NewFrameStream wraps a Yamux stream into a FrameStream.
func NewFrameStream(raw *yamux.Stream, cfg Config) *FrameStream {
	return &FrameStream{
		raw:  raw,
		done: make(chan struct{}),
		cfg:  cfg,
		log:  logger.GetLogger("stream").With().Uint32("stream_id", raw.StreamID()).Logger(),
	}
}

// ReceiveHandshake returns the already-negotiated session parameters.
// Deprecated: Mux validates negotiation and routing before publishing streams.
func (fs *FrameStream) ReceiveHandshake() (*NegotiatedParams, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.negotiated == nil {
		return nil, fmt.Errorf("transport: session has not been negotiated")
	}
	params := *fs.negotiated
	return &params, nil
}

// WriteMessage encodes and writes a message to the stream.
func (fs *FrameStream) WriteMessage(msg any) error {
	return fs.WriteMessageContext(context.Background(), msg)
}

// WriteMessageContext allows a canceled task to leave an application-level
// pause. Unpaused writes still drain queued terminal messages during shutdown.
func (fs *FrameStream) WriteMessageContext(ctx context.Context, msg any) error {
	fs.writeMu.Lock()
	defer fs.writeMu.Unlock()
	for {
		fs.mu.Lock()
		ended, resume := fs.ended, fs.resume
		fs.mu.Unlock()
		if ended {
			return fmt.Errorf("transport: stream ended, cannot write")
		}
		if resume == nil {
			break
		}
		select {
		case <-resume:
		case <-ctx.Done():
			return ctx.Err()
		case <-fs.done:
			return fmt.Errorf("transport: stream closed")
		case <-fs.session.yamux.CloseChan():
			return fmt.Errorf("transport: session closed")
		}
	}
	if fs.header != nil {
		if !fs.sender {
			return fmt.Errorf("transport: data stream is receive-only")
		}
		switch msg.(type) {
		case *protocol.DataRecordMsg, protocol.DataRecordMsg, *protocol.CheckpointBarrierMsg, protocol.CheckpointBarrierMsg, *protocol.WatermarkMsg, protocol.WatermarkMsg, *protocol.EndOfPartitionMsg, protocol.EndOfPartitionMsg:
		default:
			return fmt.Errorf("transport: message is not permitted on a data stream")
		}
	}
	if err := protocol.EncodeAndWriteFrame(fs.raw, msg); err != nil {
		_ = fs.Close()
		return err
	}
	switch msg.(type) {
	case *protocol.EndOfPartitionMsg, protocol.EndOfPartitionMsg:
		fs.mu.Lock()
		fs.ended = true
		fs.mu.Unlock()
		return fs.Close()
	}
	return nil
}

// ReadMessage reads the next frame from the stream and returns the decoded message.
// It implements error counting, unknown type skipping, watermark monotonicity,
// and end-of-partition detection per WIP-01 Section 6.
// After EndOfPartition has been delivered, subsequent calls return io.EOF.
func (fs *FrameStream) ReadMessage() (any, error) {
	fs.readMu.Lock()
	defer fs.readMu.Unlock()
	select {
	case <-fs.done:
		return nil, io.EOF
	default:
	}
	// Fast path: if stream already ended, return EOF immediately
	// instead of spinning in a read loop.
	fs.mu.Lock()
	ended := fs.ended
	fs.mu.Unlock()
	if ended {
		return nil, io.EOF
	}

	for {
		frame, err := fs.readFrame()
		if err != nil {
			if err == protocol.ErrCRCMismatch {
				fs.mu.Lock()
				fs.consecutiveCRCErrors++
				fs.consecutiveDecErrors = 0
				shouldClose := fs.consecutiveCRCErrors >= MaxConsecutiveCRCErrors
				fs.mu.Unlock()
				if shouldClose {
					fs.log.Error().Msg("closing stream: too many consecutive CRC errors")
					_ = fs.Close()
					return nil, fmt.Errorf("transport: stream closed after %d consecutive CRC errors", MaxConsecutiveCRCErrors)
				}
				fs.log.Warn().Err(err).Msg("CRC mismatch, dropping frame")
				continue
			}
			_ = fs.Close()
			return nil, err
		}

		fs.mu.Lock()
		fs.consecutiveCRCErrors = 0
		fs.mu.Unlock()
		// Decode payload.
		decoded, err := protocol.DecodePayload(frame)
		if err != nil {
			if errors.Is(err, protocol.ErrUnknownMsgType) {
				fs.mu.Lock()
				fs.consecutiveDecErrors = 0
				fs.mu.Unlock()
				fs.log.Warn().Uint8("msg_type", frame.MsgType).Msg("unknown message type, skipping frame")
				continue
			}
			fs.mu.Lock()
			fs.consecutiveDecErrors++
			shouldClose := fs.consecutiveDecErrors >= MaxConsecutiveDecodeErrors
			fs.mu.Unlock()
			if shouldClose {
				fs.log.Error().Msg("closing stream: too many consecutive decode errors")
				_ = fs.Close()
				return nil, fmt.Errorf("transport: stream closed after %d consecutive decode errors", MaxConsecutiveDecodeErrors)
			}
			fs.log.Warn().Err(err).Msg("decode error, dropping frame")
			continue
		}

		if fs.header != nil {
			valid := frame.MsgType >= protocol.MsgTypeDataRecord && frame.MsgType <= protocol.MsgTypeEndOfPartition
			if fs.sender {
				eop, ok := decoded.(*protocol.EndOfPartitionMsg)
				valid = ok && eop.Reason == protocol.EndReasonError
			}
			if !valid {
				_ = fs.Close()
				return nil, fmt.Errorf("transport: invalid data-stream message %s", protocol.MsgTypeName(frame.MsgType))
			}
		}

		// Reset error counters on success.
		fs.mu.Lock()
		fs.consecutiveCRCErrors = 0
		fs.consecutiveDecErrors = 0
		fs.mu.Unlock()

		// Check stream-ended state — drop frames received after EOP was delivered.
		fs.mu.Lock()
		alreadyEnded := fs.ended
		fs.mu.Unlock()
		if alreadyEnded {
			fs.log.Warn().Str("type", protocol.MsgTypeName(frame.MsgType)).Msg("frame after EndOfPartition, dropping")
			return nil, io.EOF
		}

		// Watermark monotonicity check (per-SourceID).
		if wm, ok := decoded.(*protocol.WatermarkMsg); ok {
			fs.mu.Lock()
			if fs.lastWatermarks == nil {
				fs.lastWatermarks = make(map[string]int64)
			}
			last, exists := fs.lastWatermarks[wm.SourceID]
			if exists && wm.Timestamp < last {
				fs.mu.Unlock()
				fs.log.Warn().
					Int64("received", wm.Timestamp).
					Int64("last", last).
					Str("source_id", wm.SourceID).
					Msg("backward watermark, dropping")
				continue
			}
			fs.lastWatermarks[wm.SourceID] = wm.Timestamp
			fs.mu.Unlock()
		}

		// EndOfPartition detection.
		if _, ok := decoded.(*protocol.EndOfPartitionMsg); ok {
			fs.mu.Lock()
			fs.ended = true
			fs.mu.Unlock()
			_ = fs.Close()
		}

		return decoded, nil
	}
}

// StreamID returns the Yamux stream ID.
func (fs *FrameStream) StreamID() uint32 {
	return fs.raw.StreamID()
}

// Close closes the underlying Yamux stream.
func (fs *FrameStream) Close() error {
	var err error
	fs.closeOnce.Do(func() {
		close(fs.done)
		// Yamux Close is a half-close; interrupt a local blocked read explicitly.
		_ = fs.raw.SetReadDeadline(time.Now())
		if fs.session != nil && fs.sender {
			fs.session.mu.Lock()
			delete(fs.session.outputs, fs.StreamID())
			fs.session.mu.Unlock()
		}
		err = fs.raw.Close()
	})
	return err
}
