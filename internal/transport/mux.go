package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/protocol"
)

// Mux is the top-level multiplexer that manages TCP/TLS connections,
// Yamux sessions, and stream lifecycle.
type Mux struct {
	mu          sync.RWMutex
	dialing     map[string]chan struct{}
	closeOnce   sync.Once
	taskChanged chan struct{}
	tasks       map[string]*taskQueue
	cfg         Config
	listener    net.Listener
	peers       map[string]*Session
	sessions    map[*Session]struct{}
	nodes       map[string]*Session
	changed     chan struct{}
	streamCh    chan *FrameStream
	log         zerolog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup // tracks acceptLoop and sessionAcceptLoop goroutines
}

// NewMux creates a new Mux with the given configuration.
func NewMux(cfg Config) *Mux {
	if cfg.NodeID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			panic(err)
		}
		cfg.NodeID = hex.EncodeToString(id[:])
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Mux{
		cfg:         cfg,
		peers:       make(map[string]*Session),
		sessions:    make(map[*Session]struct{}),
		nodes:       make(map[string]*Session),
		changed:     make(chan struct{}),
		dialing:     make(map[string]chan struct{}),
		tasks:       make(map[string]*taskQueue),
		taskChanged: make(chan struct{}),
		streamCh:    make(chan *FrameStream, 64),
		log:         logger.GetLogger("mux"),
		ctx:         ctx,
		cancel:      cancel,
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
	if m.ctx.Err() != nil || m.listener != nil {
		m.mu.Unlock()
		_ = ln.Close()
		return fmt.Errorf("transport: mux closed or already listening")
	}
	m.listener = ln
	m.wg.Add(1)
	m.mu.Unlock()

	m.log.Info().Str("addr", ln.Addr().String()).Msg("listening")

	go m.acceptLoop(ctx)
	return nil
}

// Dial opens a new FrameStream to the given address.
// It reuses an existing Yamux session if one exists, or creates a new one.
// Session negotiation completes before the routing header is sent.
func (m *Mux) Dial(ctx context.Context, addr string, routing ...protocol.StreamHeaderMsg) (*FrameStream, error) {
	header := protocol.StreamHeaderMsg{SourceTaskID: m.cfg.NodeID, TargetTaskID: "default"}
	if len(routing) > 1 {
		return nil, fmt.Errorf("transport: exactly one routing header is permitted")
	}
	if len(routing) == 1 {
		header = routing[0]
	}
	for {
		sess, err := m.getOrCreateSession(ctx, addr)
		if err != nil {
			return nil, err
		}
		stream, err := sess.OpenDataStream(ctx, m.cfg, header)
		if errors.Is(err, errSessionDraining) {
			continue
		}
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.ctx.Err() != nil {
			m.mu.Unlock()
			_ = stream.Close()
			return nil, m.ctx.Err()
		}
		stream.senderReadDone = make(chan struct{})
		stream.managedSender = true
		m.wg.Add(1)
		m.mu.Unlock()
		go func() { defer m.wg.Done(); stream.watchRejection() }()
		return stream, nil
	}
}

// RegisterTask creates a bounded incoming-stream queue for a task. Register
// before upstream connections are opened. Repeated registrations are rejected.
func (m *Mux) RegisterTask(taskID string) error {
	return m.registerTask(taskID, 64, false, nil)
}

// RegisterTaskInputs reserves room for the deployment's expected inputs before
// restore begins. Excess streams are rejected without blocking peer acceptance.
func (m *Mux) RegisterTaskInputs(taskID string, inputs int) error {
	if inputs < 1 {
		return fmt.Errorf("transport: positive input count required")
	}
	return m.registerTask(taskID, inputs, true, nil)
}

func (m *Mux) registerTask(taskID string, capacity int, rejectOverflow bool, authorize func(string, protocol.StreamHeaderMsg) bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return fmt.Errorf("transport: mux closed")
	}
	if taskID == "" || taskID == "default" {
		return fmt.Errorf("transport: invalid task ID")
	}
	if _, exists := m.tasks[taskID]; exists {
		return fmt.Errorf("transport: task already registered")
	}
	queue := newTaskQueue()
	queue.streams = make(chan *FrameStream, capacity)
	queue.rejectOverflow = rejectOverflow
	queue.authorize = authorize
	m.tasks[taskID] = queue
	close(m.taskChanged)
	m.taskChanged = make(chan struct{})
	return nil
}

// AcceptTask returns a stream whose validated header names this task.
func (m *Mux) AcceptTask(ctx context.Context, taskID string) (*FrameStream, error) {
	m.mu.RLock()
	queue, ok := m.tasks[taskID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("transport: task is not registered")
	}
	return queue.accept(ctx, m.ctx)
}

// Accept returns the next incoming FrameStream from any session.
// The session and default-task routing header have already been validated.
func (m *Mux) Accept(ctx context.Context) (*FrameStream, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.ctx.Done():
		return nil, fmt.Errorf("transport: mux closed")
	case fs, ok := <-m.streamCh:
		if !ok {
			return nil, fmt.Errorf("transport: mux closed")
		}
		return fs, nil
	}
}

// Close closes the listener and all sessions.
func (m *Mux) Close() error {
	m.closeOnce.Do(func() {
		m.cancel()
		m.mu.Lock()
		if m.listener != nil {
			_ = m.listener.Close()
		}
		for sess := range m.sessions {
			_ = sess.Close()
		}
		clear(m.peers)
		clear(m.sessions)
		clear(m.nodes)
		m.mu.Unlock()
		m.wg.Wait()
		close(m.streamCh)
	})
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
			stop := context.AfterFunc(m.ctx, func() { _ = c.Close() })
			defer stop()
			defer c.Close()
			sess, err := NewServerSession(c, m.cfg)
			if err != nil {
				m.log.Error().Err(err).Msg("server session creation failed")
				return
			}

			if _, err := sess.NegotiateSession(m.ctx, m.sessionConfig(), false); err != nil {
				m.log.Debug().Err(err).Msg("session negotiation failed")
				return
			}
			addr := sess.Addr()
			if endpoint := sess.peerListenAddress(); endpoint != "" {
				addr = endpoint
			}
			m.mu.Lock()
			if m.ctx.Err() != nil {
				m.mu.Unlock()
				_ = sess.Close()
				return
			}
			m.publishSession(addr, sess)
			m.mu.Unlock()
			defer m.forgetSession(sess)

			m.sessionAcceptLoop(ctx, sess)
		}(conn)
	}
}

func (m *Mux) sessionAcceptLoop(ctx context.Context, sess *Session) {
	for {
		var target *taskQueue
		fs, err := sess.AcceptDataStream(m.cfg, func(header protocol.StreamHeaderMsg) bool {
			if header.TargetTaskID == "default" {
				return true
			}
			target = m.waitForTask(ctx, sess, header.TargetTaskID)
			if target == nil {
				return false
			}
			_, peerID, negotiated := sess.SessionParameters()
			return negotiated && (target.authorize == nil || target.authorize(peerID, header))
		})
		if err != nil {
			if !sess.IsClosed() && m.ctx.Err() == nil && ctx.Err() == nil {
				continue
			}
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

		if target != nil {
			if err := target.enqueue(m.ctx, fs); err != nil {
				_ = fs.Close()
			}
			continue
		}
		queue := m.streamCh
		select {
		case queue <- fs:
		case <-ctx.Done():
			_ = fs.Close()
			return
		case <-m.ctx.Done():
			_ = fs.Close()
			return
		}
	}
}

func (m *Mux) getOrCreateSession(ctx context.Context, addr string) (*Session, error) {
	// Coalesce same-peer dials without holding a global lock across network
	// I/O. Waiters may cancel and unrelated workers can connect independently.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.ctx.Err() != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("transport: mux closed")
		}
		if sess := m.peers[addr]; sess != nil && !sess.IsClosed() {
			if sess.isDraining() {
				changed := m.changed
				m.mu.Unlock()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-m.ctx.Done():
					return nil, m.ctx.Err()
				case <-changed:
					continue
				}
			}
			m.mu.Unlock()
			return sess, nil
		}
		if pending := m.dialing[addr]; pending != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-m.ctx.Done():
				return nil, fmt.Errorf("transport: mux closed")
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		m.dialing[addr] = pending
		m.mu.Unlock()
		defer func() { m.mu.Lock(); delete(m.dialing, addr); close(pending); m.mu.Unlock() }()
		break
	}
	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopDial := context.AfterFunc(m.ctx, cancel)
	defer stopDial()
	sess, err := NewClientSessionContext(dialCtx, addr, m.cfg)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(m.ctx, func() { _ = sess.Close() })
	defer stop()
	if _, err := sess.NegotiateSession(ctx, m.sessionConfig(), true); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("transport: mux closed")
	}
	selected := m.publishSession(addr, sess)
	// Yamux permits the accepting peer to open its own unidirectional data
	// streams on this connection. Service those streams for the lifetime of
	// the mux, independently of the caller that initiated the connection.
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.forgetSession(sess)
		m.sessionAcceptLoop(m.ctx, sess)
	}()
	return selected, nil
}

// waitForTask uses one bounded wait in the session accept loop, without
// allocating a goroutine or a queue for an unregistered task.
func (m *Mux) waitForTask(ctx context.Context, sess *Session, id string) *taskQueue {
	var timer *time.Timer
	for {
		m.mu.RLock()
		queue, changed := m.tasks[id], m.taskChanged
		m.mu.RUnlock()
		if queue != nil {
			return queue
		}
		if m.cfg.TaskRegistrationTimeout <= 0 {
			return nil
		}
		if timer == nil {
			timer = time.NewTimer(m.cfg.TaskRegistrationTimeout)
			defer timer.Stop()
		}
		select {
		case <-changed:
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return nil
		case <-m.ctx.Done():
			return nil
		case <-sess.yamux.CloseChan():
			return nil
		}
	}
}
