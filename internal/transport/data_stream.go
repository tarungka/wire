package transport

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/yamux"

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
	raw, err := s.openStreamContext(deadline)
	if err != nil {
		return nil, sessionHandshakeError(deadline, err)
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

// openStreamContext isolates cancellation from other tasks on the session.
// Yamux has no context-aware open. Permit at most one pending open worker per
// session; cancelled callers return immediately, and a late stream is closed.
// Session shutdown releases a worker blocked in Yamux's SYN backlog.
func (s *Session) openStreamContext(ctx context.Context) (*yamux.Stream, error) {
	s.dataMu.Lock()
	if s.openGate == nil {
		s.openGate = make(chan struct{}, 1)
	}
	gate := s.openGate
	s.dataMu.Unlock()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.dataMu.Lock()
	s.opening++
	s.dataMu.Unlock()
	type result struct {
		stream *yamux.Stream
		err    error
	}
	ready := make(chan result)
	go func() {
		defer func() { <-gate; s.dataMu.Lock(); s.opening--; s.dataMu.Unlock() }()
		raw, err := s.OpenStream()
		select {
		case ready <- result{raw, err}:
		case <-ctx.Done():
			if raw != nil {
				_ = raw.Close()
			}
		}
	}()
	select {
	case got := <-ready:
		if err := ctx.Err(); err != nil {
			if got.stream != nil {
				_ = got.stream.Close()
			}
			return nil, err
		}
		return got.stream, got.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
