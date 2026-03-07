package transport

import (
	"errors"
	"fmt"
	"io"
	"net"
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
		raw: raw,
		cfg: cfg,
		log: logger.GetLogger("stream").With().Uint32("stream_id", raw.StreamID()).Logger(),
	}
}

// SendHandshake writes a Handshake frame with the local version and features.
func (fs *FrameStream) SendHandshake() error {
	msg := &protocol.HandshakeMsg{
		ProtocolVersion: fs.cfg.LocalProtocolVersion,
		MinVersion:      fs.cfg.LocalMinVersion,
		Features:        fs.cfg.LocalFeatures,
	}
	return protocol.WriteFrame(fs.raw, protocol.MsgTypeHandshake, msg)
}

// ReceiveHandshake reads and validates the first frame on a stream.
// It sets a read deadline for the handshake timeout.
// On incompatible version, it sends EndOfPartition(Error) and returns ErrVersionIncompatible.
func (fs *FrameStream) ReceiveHandshake() (*NegotiatedParams, error) {
	// Set read deadline for handshake.
	_ = fs.raw.SetReadDeadline(time.Now().Add(fs.cfg.HandshakeTimeout))
	defer func() { _ = fs.raw.SetReadDeadline(time.Time{}) }() // Clear deadline after handshake.

	frame, err := protocol.ReadFrame(fs.raw, fs.cfg.MaxFrameSize)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, fmt.Errorf("%w: %v", protocol.ErrHandshakeTimeout, err)
		}
		return nil, fmt.Errorf("transport: handshake read failed: %w", err)
	}

	if frame.MsgType != protocol.MsgTypeHandshake {
		return nil, protocol.ErrHandshakeExpected
	}

	decoded, err := protocol.DecodePayload(frame)
	if err != nil {
		return nil, err
	}

	hs := decoded.(*protocol.HandshakeMsg)

	// Check version compatibility per WIP-01 Section 3.8.
	if hs.ProtocolVersion < fs.cfg.LocalMinVersion || fs.cfg.LocalProtocolVersion < hs.MinVersion {
		// Send EndOfPartition with Error reason before closing.
		eop := &protocol.EndOfPartitionMsg{
			SourceID: protocol.HandshakeSourceID,
			Reason:   protocol.EndReasonError,
		}
		_ = protocol.WriteFrame(fs.raw, protocol.MsgTypeEndOfPartition, eop)
		return nil, protocol.ErrVersionIncompatible
	}

	// Negotiate effective version and features.
	effectiveVersion := hs.ProtocolVersion
	if fs.cfg.LocalProtocolVersion < effectiveVersion {
		effectiveVersion = fs.cfg.LocalProtocolVersion
	}
	features := hs.Features & fs.cfg.LocalFeatures

	params := &NegotiatedParams{
		EffectiveVersion: effectiveVersion,
		Features:         features,
	}
	fs.mu.Lock()
	fs.negotiated = params
	fs.mu.Unlock()

	return params, nil
}

// WriteMessage encodes and writes a message to the stream.
func (fs *FrameStream) WriteMessage(msg any) error {
	fs.mu.Lock()
	ended := fs.ended
	fs.mu.Unlock()
	if ended {
		return fmt.Errorf("transport: stream ended, cannot write")
	}
	return protocol.EncodeAndWriteFrame(fs.raw, msg)
}

// ReadMessage reads the next frame from the stream and returns the decoded message.
// It implements error counting, unknown type skipping, watermark monotonicity,
// and end-of-partition detection per WIP-01 Section 6.
// After EndOfPartition has been delivered, subsequent calls return io.EOF.
func (fs *FrameStream) ReadMessage() (any, error) {
	// Fast path: if stream already ended, return EOF immediately
	// instead of spinning in a read loop.
	fs.mu.Lock()
	ended := fs.ended
	fs.mu.Unlock()
	if ended {
		return nil, io.EOF
	}

	for {
		frame, err := protocol.ReadFrame(fs.raw, fs.cfg.MaxFrameSize)
		if err != nil {
			if err == protocol.ErrCRCMismatch {
				fs.mu.Lock()
				fs.consecutiveCRCErrors++
				shouldClose := fs.consecutiveCRCErrors >= MaxConsecutiveCRCErrors
				fs.mu.Unlock()
				if shouldClose {
					fs.log.Error().Msg("closing stream: too many consecutive CRC errors")
					_ = fs.raw.Close()
					return nil, fmt.Errorf("transport: stream closed after %d consecutive CRC errors", MaxConsecutiveCRCErrors)
				}
				fs.log.Warn().Err(err).Msg("CRC mismatch, dropping frame")
				continue
			}
			return nil, err
		}

		// Decode payload.
		decoded, err := protocol.DecodePayload(frame)
		if err != nil {
			if errors.Is(err, protocol.ErrUnknownMsgType) {
				fs.log.Warn().Uint8("msg_type", frame.MsgType).Msg("unknown message type, skipping frame")
				continue
			}
			fs.mu.Lock()
			fs.consecutiveDecErrors++
			shouldClose := fs.consecutiveDecErrors >= MaxConsecutiveDecodeErrors
			fs.mu.Unlock()
			if shouldClose {
				fs.log.Error().Msg("closing stream: too many consecutive decode errors")
				_ = fs.raw.Close()
				return nil, fmt.Errorf("transport: stream closed after %d consecutive decode errors", MaxConsecutiveDecodeErrors)
			}
			fs.log.Warn().Err(err).Msg("decode error, dropping frame")
			continue
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
	return fs.raw.Close()
}
