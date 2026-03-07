package transport

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/logger"
)

// Mux is the top-level multiplexer that manages TCP/TLS connections,
// Yamux sessions, and stream lifecycle.
type Mux struct {
	mu       sync.RWMutex
	cfg      Config
	listener net.Listener
	peers    map[string]*Session
	streamCh chan *FrameStream
	log      zerolog.Logger
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup // tracks acceptLoop and sessionAcceptLoop goroutines
}

// NewMux creates a new Mux with the given configuration.
func NewMux(cfg Config) *Mux {
	ctx, cancel := context.WithCancel(context.Background())
	return &Mux{
		cfg:      cfg,
		peers:    make(map[string]*Session),
		streamCh: make(chan *FrameStream, 64),
		log:      logger.GetLogger("mux"),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Listen starts a TCP/TLS listener and accepts incoming connections.
func (m *Mux) Listen(ctx context.Context) error {
	// Always listen on raw TCP. TLS wrapping is handled per-connection in NewServerSession.
	ln, err := net.Listen("tcp", m.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("transport: listen on %s: %w", m.cfg.ListenAddr, err)
	}

	m.mu.Lock()
	m.listener = ln
	m.mu.Unlock()

	m.log.Info().Str("addr", ln.Addr().String()).Msg("listening")

	m.wg.Add(1)
	go m.acceptLoop(ctx)
	return nil
}

// Dial opens a new FrameStream to the given address.
// It reuses an existing Yamux session if one exists, or creates a new one.
// The handshake is automatically sent on the new stream.
func (m *Mux) Dial(ctx context.Context, addr string) (*FrameStream, error) {
	sess, err := m.getOrCreateSession(addr)
	if err != nil {
		return nil, err
	}

	raw, err := sess.OpenStream()
	if err != nil {
		// Session may be dead; remove and retry once.
		m.mu.Lock()
		delete(m.peers, addr)
		m.mu.Unlock()
		_ = sess.Close()

		sess, err = m.getOrCreateSession(addr)
		if err != nil {
			return nil, err
		}
		raw, err = sess.OpenStream()
		if err != nil {
			return nil, fmt.Errorf("transport: open stream to %s: %w", addr, err)
		}
	}

	fs := NewFrameStream(raw, m.cfg)
	if err := fs.SendHandshake(); err != nil {
		_ = fs.Close()
		return nil, fmt.Errorf("transport: handshake to %s: %w", addr, err)
	}

	return fs, nil
}

// Accept returns the next incoming FrameStream from any session.
// The handshake has NOT yet been received — the caller should call
// ReceiveHandshake() on the returned stream.
func (m *Mux) Accept(ctx context.Context) (*FrameStream, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case fs, ok := <-m.streamCh:
		if !ok {
			return nil, fmt.Errorf("transport: mux closed")
		}
		return fs, nil
	}
}

// OpenControlStream opens a dedicated stream for backpressure control traffic.
func (m *Mux) OpenControlStream(ctx context.Context, addr string) (*FrameStream, error) {
	return m.Dial(ctx, addr)
}

// Close closes the listener and all sessions.
func (m *Mux) Close() error {
	m.cancel()

	m.mu.Lock()
	if m.listener != nil {
		_ = m.listener.Close()
	}
	for addr, sess := range m.peers {
		_ = sess.Close()
		delete(m.peers, addr)
	}
	m.mu.Unlock()

	// Wait for all accept goroutines to exit before closing the channel
	// to prevent send-on-closed-channel panics.
	m.wg.Wait()
	close(m.streamCh)
	return nil
}

// ListenAddr returns the address the mux is listening on, or empty if not listening.
func (m *Mux) ListenAddr() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.listener != nil {
		return m.listener.Addr().String()
	}
	return ""
}

func (m *Mux) acceptLoop(ctx context.Context) {
	defer m.wg.Done()
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-m.ctx.Done():
				return
			default:
				m.log.Error().Err(err).Msg("accept failed")
				continue
			}
		}

		m.wg.Add(1)
		go func(c net.Conn) {
			defer m.wg.Done()
			sess, err := NewServerSession(c, m.cfg)
			if err != nil {
				m.log.Error().Err(err).Msg("server session creation failed")
				return
			}

			addr := sess.Addr()
			m.mu.Lock()
			m.peers[addr] = sess
			m.mu.Unlock()

			m.sessionAcceptLoop(ctx, sess)
		}(conn)
	}
}

func (m *Mux) sessionAcceptLoop(ctx context.Context, sess *Session) {
	for {
		raw, err := sess.AcceptStream()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-m.ctx.Done():
				return
			default:
				if !sess.IsClosed() {
					m.log.Debug().Err(err).Str("addr", sess.Addr()).Msg("stream accept ended")
				}
				return
			}
		}

		fs := NewFrameStream(raw, m.cfg)
		select {
		case m.streamCh <- fs:
		case <-ctx.Done():
			_ = fs.Close()
			return
		case <-m.ctx.Done():
			_ = fs.Close()
			return
		}
	}
}

func (m *Mux) getOrCreateSession(addr string) (*Session, error) {
	m.mu.RLock()
	if sess, ok := m.peers[addr]; ok {
		m.mu.RUnlock()
		if !sess.IsClosed() {
			return sess, nil
		}
		// Session is closed, need to create a new one.
		m.mu.Lock()
		delete(m.peers, addr)
		m.mu.Unlock()
	} else {
		m.mu.RUnlock()
	}

	sess, err := NewClientSession(addr, m.cfg)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	// Double-check — another goroutine may have created one.
	if existing, ok := m.peers[addr]; ok && !existing.IsClosed() {
		m.mu.Unlock()
		_ = sess.Close()
		return existing, nil
	}
	m.peers[addr] = sess
	m.mu.Unlock()

	return sess, nil
}
