package transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/protocol"
)

// NegotiateSession exchanges WIP-01 SessionHandshake frames on the first
// control stream. Call before opening or accepting any data streams. RPC-only
// sessions have their separate WIP-07 framing and do not invoke this method.
// Any failed negotiation tears down the entire session.
func (s *Session) NegotiateSession(ctx context.Context, cfg Config, initiator bool) (_ NegotiatedParams, retErr error) {
	s.negotiationMu.Lock()
	defer s.negotiationMu.Unlock()
	if s.negotiated != nil {
		return *s.negotiated, nil
	}
	defer func() {
		if retErr != nil {
			_ = s.Close()
		}
	}()
	if cfg.NodeID == "" || cfg.LocalMinVersion == 0 || cfg.LocalProtocolVersion < cfg.LocalMinVersion {
		return NegotiatedParams{}, fmt.Errorf("transport: invalid local session identity or version range")
	}
	timeout := cfg.HandshakeTimeout
	if timeout <= 0 {
		timeout = DefaultHandshakeTimeout
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stop := context.AfterFunc(deadline, func() { _ = s.Close() })
	defer stop()
	var stream *yamux.Stream
	var err error
	if initiator {
		stream, err = s.OpenStream()
	} else {
		stream, err = s.AcceptStream()
	}
	if err != nil {
		return NegotiatedParams{}, sessionHandshakeError(deadline, err)
	}
	until, _ := deadline.Deadline()
	if err = stream.SetDeadline(until); err != nil {
		return NegotiatedParams{}, err
	}
	local := protocol.SessionHandshakeMsg{ProtocolVersion: cfg.LocalProtocolVersion, MinVersion: cfg.LocalMinVersion, Features: cfg.LocalFeatures, NodeID: cfg.NodeID, ListenPort: cfg.sessionListenPort}
	send := func() error { return protocol.EncodeAndWriteFrameLimit(stream, &local, cfg.MaxFrameSize) }
	receive := func() (*protocol.SessionHandshakeMsg, error) {
		frame, err := protocol.ReadFrame(stream, cfg.MaxFrameSize)
		if err != nil {
			return nil, err
		}
		if frame.MsgType != protocol.MsgTypeSessionHandshake {
			return nil, protocol.ErrHandshakeExpected
		}
		message, err := protocol.DecodePayload(frame)
		if err != nil {
			return nil, err
		}
		return message.(*protocol.SessionHandshakeMsg), nil
	}
	if initiator {
		if err = send(); err != nil {
			return NegotiatedParams{}, sessionHandshakeError(deadline, err)
		}
	}
	remote, err := receive()
	if err != nil {
		return NegotiatedParams{}, sessionHandshakeError(deadline, err)
	}
	if remote.NodeID == "" || remote.MinVersion == 0 || remote.ProtocolVersion < remote.MinVersion {
		return NegotiatedParams{}, fmt.Errorf("transport: invalid remote session identity or version range")
	}
	effective := min(local.ProtocolVersion, remote.ProtocolVersion)
	incompatible := effective < local.MinVersion || effective < remote.MinVersion
	// Respond before rejecting an incompatible range so both peers can diagnose it.
	if !initiator {
		if err = send(); err != nil {
			if incompatible {
				// The peer may receive our reply and close before Yamux's
				// Write returns. The validated version mismatch remains true.
				return NegotiatedParams{}, errors.Join(protocol.ErrVersionIncompatible, sessionHandshakeError(deadline, err))
			}
			return NegotiatedParams{}, sessionHandshakeError(deadline, err)
		}
	}
	if incompatible {
		return NegotiatedParams{}, protocol.ErrVersionIncompatible
	}
	if err = stream.SetDeadline(time.Time{}); err != nil {
		return NegotiatedParams{}, err
	}
	if !stop() || deadline.Err() != nil {
		return NegotiatedParams{}, sessionHandshakeError(deadline, deadline.Err())
	}
	params := NegotiatedParams{EffectiveVersion: effective, Features: local.Features & remote.Features}
	s.negotiated = &params
	s.control = stream
	s.peerDrained = make(chan struct{})
	s.peerNodeID = remote.NodeID
	s.peerListenPort = remote.ListenPort
	s.initiator = initiator
	go s.runControl(cfg)
	return params, nil
}

func sessionHandshakeError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", protocol.ErrHandshakeTimeout, err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// A stream deadline may win the race with the context timer.
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return fmt.Errorf("%w: %v", protocol.ErrHandshakeTimeout, err)
	}
	return fmt.Errorf("transport: session handshake: %w", err)
}

// SessionParameters returns a copy of the negotiated state, when available.
func (s *Session) SessionParameters() (NegotiatedParams, string, bool) {
	s.negotiationMu.Lock()
	defer s.negotiationMu.Unlock()
	if s.negotiated == nil {
		return NegotiatedParams{}, "", false
	}
	return *s.negotiated, s.peerNodeID, true
}
