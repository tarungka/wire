package transport

import (
	"context"
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// OpenDataStream opens a sender-only stream after session negotiation. The
// header is written before the stream becomes available to the producer.
func (s *Session) OpenDataStream(ctx context.Context, cfg Config, header protocol.StreamHeaderMsg) (*FrameStream, error) {
	s.dataMu.Lock()
	if s.draining {
		s.dataMu.Unlock()
		return nil, errSessionDraining
	}
	s.opening++
	s.dataMu.Unlock()
	defer func() { s.dataMu.Lock(); s.opening--; s.dataMu.Unlock() }()
	params, _, ok := s.SessionParameters()
	if !ok {
		return nil, fmt.Errorf("transport: session has not been negotiated")
	}
	if header.SourceTaskID == "" || header.TargetTaskID == "" {
		return nil, fmt.Errorf("transport: source and target task IDs are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := cfg.HandshakeTimeout
	if timeout <= 0 {
		timeout = DefaultHandshakeTimeout
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Yamux OpenStream can block on its backlog and has no context API.
	// Closing the session is necessary to interrupt an abandoned open.
	stopOpen := context.AfterFunc(deadline, func() { _ = s.Close() })
	raw, err := s.OpenStream()
	if !stopOpen() || deadline.Err() != nil {
		if raw != nil {
			_ = raw.Close()
		}
		return nil, sessionHandshakeError(deadline, deadline.Err())
	}
	if err != nil {
		return nil, err
	}
	fs := NewFrameStream(raw, cfg)
	fs.negotiated = &params
	fs.header = &header
	fs.session = s
	fs.sender = true
	s.mu.Lock()
	if s.outputs == nil {
		s.outputs = make(map[uint32]*FrameStream)
	}
	s.outputs[raw.StreamID()] = fs
	s.mu.Unlock()
	published := false
	defer func() {
		if !published {
			_ = fs.Close()
		}
	}()
	stop := context.AfterFunc(deadline, func() { _ = raw.Close() })
	defer stop()
	until, _ := deadline.Deadline()
	_ = raw.SetWriteDeadline(until)
	if err := protocol.EncodeAndWriteFrameLimit(raw, &header, cfg.MaxFrameSize); err != nil {
		_ = raw.Close()
		return nil, sessionHandshakeError(deadline, err)
	}
	if !stop() || deadline.Err() != nil {
		_ = raw.Close()
		return nil, sessionHandshakeError(deadline, deadline.Err())
	}
	_ = raw.SetWriteDeadline(time.Time{})
	published = true
	return fs, nil
}

// AcceptDataStream validates the first data frame and target before publishing
// the stream. A rejected target receives the sole permitted reverse-direction
// data-stream frame, EndOfPartition(Error), followed by stream closure.
func (s *Session) AcceptDataStream(cfg Config, targetExists func(protocol.StreamHeaderMsg) bool) (*FrameStream, error) {
	params, _, ok := s.SessionParameters()
	if !ok {
		return nil, fmt.Errorf("transport: session has not been negotiated")
	}
	raw, err := s.AcceptStream()
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = raw.Close()
		}
	}()
	timeout := cfg.HandshakeTimeout
	if timeout <= 0 {
		timeout = DefaultHandshakeTimeout
	}
	_ = raw.SetDeadline(time.Now().Add(timeout))
	frame, err := protocol.ReadFrame(raw, cfg.MaxFrameSize)
	if err != nil {
		return nil, sessionHandshakeError(context.Background(), err)
	}
	if frame.MsgType != protocol.MsgTypeStreamHeader {
		return nil, protocol.ErrHandshakeExpected
	}
	msg, err := protocol.DecodePayload(frame)
	if err != nil {
		return nil, err
	}
	header := msg.(*protocol.StreamHeaderMsg)
	if header.SourceTaskID == "" || header.TargetTaskID == "" {
		return nil, fmt.Errorf("transport: source and target task IDs are required")
	}
	if targetExists == nil || !targetExists(*header) {
		_ = protocol.EncodeAndWriteFrameLimit(raw, &protocol.EndOfPartitionMsg{SourceID: header.TargetTaskID, Reason: protocol.EndReasonError}, cfg.MaxFrameSize)
		return nil, fmt.Errorf("transport: unknown target task %q", header.TargetTaskID)
	}
	_ = raw.SetDeadline(time.Time{})
	fs := NewFrameStream(raw, cfg)
	fs.negotiated = &params
	fs.header = header
	fs.readOffset = uint64(frame.Length) + protocol.LengthFieldSize
	fs.session = s
	accepted = true
	return fs, nil
}

// Header returns a copy of the stream's routing metadata.
func (fs *FrameStream) Header() (protocol.StreamHeaderMsg, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.header == nil {
		return protocol.StreamHeaderMsg{}, false
	}
	return *fs.header, true
}
