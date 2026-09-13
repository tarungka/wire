package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/logger"
)

// Session wraps a yamux.Session and its underlying net.Conn.
type Session struct {
	dataMu         sync.Mutex
	draining       bool
	opening        int
	peerDrained    chan struct{}
	peerDrainOnce  sync.Once
	negotiationMu  sync.Mutex
	controlWriteMu sync.Mutex
	outputs        map[uint32]*FrameStream
	negotiated     *NegotiatedParams
	control        *yamux.Stream
	peerNodeID     string
	peerListenPort uint16
	initiator      bool
	mu             sync.Mutex
	yamux          *yamux.Session
	conn           net.Conn
	addr           string
	closed         bool
	log            zerolog.Logger
}

// NewClientSession dials the given address, optionally wraps in TLS,
// and creates a Yamux client session.
func NewClientSession(addr string, cfg Config) (*Session, error) {
	return NewClientSessionContext(context.Background(), addr, cfg)
}

// NewClientSessionContext bounds connection and TLS establishment by ctx.
func NewClientSessionContext(ctx context.Context, addr string, cfg Config) (*Session, error) {
	dialTimeout := cfg.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = DefaultDialTimeout
	}
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: dial %s: %w", addr, err)
	}

	if cfg.TLSConfig != nil {
		host, _, _ := net.SplitHostPort(addr)
		tlsCfg := cfg.TLSConfig.Clone()
		if tlsCfg.ServerName == "" {
			tlsCfg.ServerName = host
		}
		if tlsCfg.MinVersion < tls.VersionTLS13 {
			tlsCfg.MinVersion = tls.VersionTLS13
		}
		tlsConn := tls.Client(conn, tlsCfg)
		timeout := cfg.HandshakeTimeout
		if timeout <= 0 {
			timeout = DefaultHandshakeTimeout
		}
		handshakeCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("transport: TLS handshake with %s: %w", addr, err)
		}
		conn = tlsConn
	}

	ymux, err := yamux.Client(conn, cfg.yamuxConfig())
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("transport: yamux client session: %w", err)
	}

	return &Session{
		yamux: ymux,
		conn:  conn,
		addr:  addr,
		log:   logger.GetLogger("session"),
	}, nil
}

// NewServerSession wraps an accepted connection, optionally with TLS,
// and creates a Yamux server session.
func NewServerSession(conn net.Conn, cfg Config) (*Session, error) {
	if cfg.TLSConfig != nil {
		tlsCfg := cfg.TLSConfig.Clone()
		if tlsCfg.MinVersion < tls.VersionTLS13 {
			tlsCfg.MinVersion = tls.VersionTLS13
		}
		tlsConn := tls.Server(conn, tlsCfg)
		timeout := cfg.HandshakeTimeout
		if timeout <= 0 {
			timeout = DefaultHandshakeTimeout
		}
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if err := tlsConn.Handshake(); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("transport: TLS server handshake: %w", err)
		}
		_ = conn.SetDeadline(time.Time{})
		conn = tlsConn
	}

	ymux, err := yamux.Server(conn, cfg.yamuxConfig())
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("transport: yamux server session: %w", err)
	}

	return &Session{
		yamux: ymux,
		conn:  conn,
		addr:  conn.RemoteAddr().String(),
		log:   logger.GetLogger("session"),
	}, nil
}

// OpenStream opens a new Yamux stream on this session.
func (s *Session) OpenStream() (*yamux.Stream, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("transport: session is closed")
	}
	// Yamux permits concurrent opens and close. Never hold our lifecycle lock
	// while waiting for its stream backlog: cancellation must be able to close.
	return s.yamux.OpenStream()
}

// AcceptStream accepts a new Yamux stream from the remote side.
func (s *Session) AcceptStream() (*yamux.Stream, error) {
	return s.yamux.AcceptStream()
}

// Close closes the Yamux session and the underlying connection.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.yamux.Close()
}

// IsClosed returns whether the session has been closed.
func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed || s.yamux.IsClosed()
}

// YamuxSession returns the underlying yamux.Session.
func (s *Session) YamuxSession() *yamux.Session {
	return s.yamux
}

// Addr returns the remote address of this session.
func (s *Session) Addr() string {
	return s.addr
}
