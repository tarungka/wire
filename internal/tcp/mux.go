package tcp

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
)

// DefaultTimeout is the default timeout for operations
const DefaultTimeout = 30 * time.Second

// Mux wraps a network listener and manages Yamux sessions.
// It serves as the entry point for internode communication.
type Mux struct {
	ln     net.Listener
	config *yamux.Config
	logger zerolog.Logger

	// peers maps remote addresses to active yamux sessions
	peers   map[string]*yamux.Session
	peersMu sync.RWMutex

	// streams is a channel of incoming accepted streams from all sessions
	streams chan net.Conn

	shutdownCh chan struct{}
}

// NewMux creates a new Mux instance.
func NewMux(ln net.Listener) (*Mux, error) {
	// Configure Yamux for high-throughput streaming
	conf := yamux.DefaultConfig()
	conf.KeepAliveInterval = 15 * time.Second
	conf.ConnectionWriteTimeout = 10 * time.Second
	conf.MaxStreamWindowSize = 1024 * 1024 // 1MB window
	conf.LogOutput = nil                   // We use our own logger

	return &Mux{
		ln:         ln,
		config:     conf,
		logger:     logger.GetLogger("mux"),
		peers:      make(map[string]*yamux.Session),
		streams:    make(chan net.Conn),
		shutdownCh: make(chan struct{}),
	}, nil
}

// NewTLSMux creates a new Mux with TLS encryption.
func NewTLSMux(ln net.Listener, certFile, keyFile, caCertFile string, clientAuth bool) (*Mux, error) {
	// Setup TLS config
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load keypair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if clientAuth {
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		// TODO: Load CA cert pool
	}

	tlsListener := tls.NewListener(ln, tlsConfig)
	return NewMux(tlsListener)
}

// Serve starts accepting connections on the listener.
// For each incoming TCP connection, it establishes a Yamux session
// and starts accepting streams from that session.
func (m *Mux) Serve() error {
	m.logger.Info().Msgf("mux serving on %s", m.ln.Addr())

	for {
		select {
		case <-m.shutdownCh:
			return nil
		default:
		}

		conn, err := m.ln.Accept()
		if err != nil {
			if isTemporary(err) {
				continue
			}
			return err
		}

		go m.handleConnection(conn)
	}
}

// handleConnection wraps an incoming TCP connection in a Yamux session.
func (m *Mux) handleConnection(conn net.Conn) {
	session, err := yamux.Server(conn, m.config)
	if err != nil {
		m.logger.Error().Err(err).Msg("failed to handshake yamux session")
		conn.Close()
		return
	}

	remoteAddr := conn.RemoteAddr().String()
	m.peersMu.Lock()
	m.peers[remoteAddr] = session
	m.peersMu.Unlock()

	m.logger.Info().Str("remote", remoteAddr).Msg("accepted new peer session")

	// Accept streams from this session until it closes
	for {
		stream, err := session.Accept()
		if err != nil {
			m.logger.Debug().Err(err).Str("remote", remoteAddr).Msg("session closed")
			m.removePeer(remoteAddr)
			return
		}

		select {
		case m.streams <- stream:
		case <-m.shutdownCh:
			stream.Close()
			return
		}
	}
}

// Accept returns the next logical stream from any connected peer.
// This matches the net.Listener interface but returns streams, not raw TCP conns.
func (m *Mux) Accept() (net.Conn, error) {
	select {
	case stream := <-m.streams:
		return stream, nil
	case <-m.shutdownCh:
		return nil, net.ErrClosed
	}
}

// Dial opens a new logical stream to the given address.
// If a session exists, it reuses it. If not, it dials TCP and establishes Yamux.
func (m *Mux) Dial(addr string, timeout time.Duration) (net.Conn, error) {
	m.peersMu.RLock()
	session, ok := m.peers[addr]
	m.peersMu.RUnlock()

	if ok && !session.IsClosed() {
		return session.Open()
	}

	// No session, dial new one
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}

	session, err = yamux.Client(conn, m.config)
	if err != nil {
		conn.Close()
		return nil, err
	}

	m.peersMu.Lock()
	m.peers[addr] = session
	m.peersMu.Unlock()

	return session.Open()
}

func (m *Mux) Close() error {
	close(m.shutdownCh)
	m.peersMu.Lock()
	defer m.peersMu.Unlock()

	for _, session := range m.peers {
		session.Close()
	}
	return m.ln.Close()
}

func (m *Mux) Addr() net.Addr {
	return m.ln.Addr()
}

func (m *Mux) removePeer(addr string) {
	m.peersMu.Lock()
	defer m.peersMu.Unlock()
	delete(m.peers, addr)
}

func isTemporary(err error) bool {
	if ne, ok := err.(net.Error); ok {
		return ne.Temporary()
	}
	return false
}
