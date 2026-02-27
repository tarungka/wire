package transport

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
)

// Session wraps a yamux.Session and its underlying net.Conn.
type Session struct {
	mu     sync.Mutex
	yamux  *yamux.Session
	conn   net.Conn
	addr   string
	closed bool
	log    zerolog.Logger
}

// NewClientSession dials the given address, optionally wraps in TLS,
// and creates a Yamux client session.
func NewClientSession(addr string, cfg Config) (*Session, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: dial %s: %w", addr, err)
	}

	if cfg.TLSConfig != nil {
		host, _, _ := net.SplitHostPort(addr)
		tlsConn := tls.Client(conn, &tls.Config{
			Certificates: cfg.TLSConfig.Certificates,
			RootCAs:      cfg.TLSConfig.RootCAs,
			MinVersion:   tls.VersionTLS13,
			ServerName:   host,
		})
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("transport: TLS handshake with %s: %w", addr, err)
		}
		conn = tlsConn
	}

	ymux, err := yamux.Client(conn, cfg.yamuxConfig())
	if err != nil {
		conn.Close()
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
		tlsConn := tls.Server(conn, cfg.TLSConfig)
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("transport: TLS server handshake: %w", err)
		}
		conn = tlsConn
	}

	ymux, err := yamux.Server(conn, cfg.yamuxConfig())
	if err != nil {
		conn.Close()
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
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("transport: session is closed")
	}
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
	return s.closed
}

// Addr returns the remote address of this session.
func (s *Session) Addr() string {
	return s.addr
}
