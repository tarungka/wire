package transport

import (
	"errors"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

var errSessionDraining = errors.New("transport: session is draining")

func (s *Session) isDraining() bool {
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	return s.draining
}

func (s *Session) beginDrain(cfg Config) {
	// Negotiated parameters are immutable after publication.
	if s.negotiated.Features&protocol.FeatureSessionDrain == 0 {
		return
	}
	s.dataMu.Lock()
	if s.draining {
		s.dataMu.Unlock()
		return
	}
	s.draining = true
	s.dataMu.Unlock()
	go s.drainSession(cfg)
}

func (s *Session) drainSession(cfg Config) {
	send := func(ready bool) error {
		s.controlWriteMu.Lock()
		defer s.controlWriteMu.Unlock()
		timeout := cfg.ConnectionWriteTimeout
		if timeout <= 0 {
			timeout = DefaultConnectionWriteTimeout
		}
		_ = s.control.SetWriteDeadline(time.Now().Add(timeout))
		defer func() { _ = s.control.SetWriteDeadline(time.Time{}) }()
		return protocol.EncodeAndWriteFrameLimit(s.control, &protocol.SessionDrainMsg{Ready: ready}, cfg.MaxFrameSize)
	}
	if err := send(false); err != nil {
		_ = s.Close()
		return
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.dataMu.Lock()
		opening := s.opening
		s.dataMu.Unlock()
		// Yamux retains half-closed data streams until the peer also closes them.
		// The control stream is the sole remaining stream at this point.
		if opening == 0 && s.yamux.NumStreams() == 1 {
			break
		}
		select {
		case <-s.yamux.CloseChan():
			return
		case <-ticker.C:
		}
	}
	if err := send(true); err != nil {
		_ = s.Close()
		return
	}
	select {
	case <-s.peerDrained:
		_ = s.Close()
	case <-s.yamux.CloseChan():
	}
}
