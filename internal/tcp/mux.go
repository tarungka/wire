package tcp

import (
	"crypto/tls"
	"errors"
	"expvar"
	// "fmt"
	// "io"
	"net"
	"os"
	"sync"
	"time"

	// "github.com/rqlite/rqlite/v8/rtls"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
)

const (
	// DefaultTimeout is the default length of time to wait for first byte.
	DefaultTimeout = 30 * time.Second
)

// stats captures stats for the mux system.
var stats *expvar.Map

const (
	numConnectionsHandled   = "num_connections_handled"
	numUnregisteredHandlers = "num_unregistered_handlers"
)

func init() {
	stats = expvar.NewMap("mux")
	stats.Add(numConnectionsHandled, 0)
	stats.Add(numUnregisteredHandlers, 0)
}

// Layer represents the connection between nodes. It can be both used to
// make connections to other nodes (client), and receive connections from other
// nodes (server)
type Layer struct {
	ln     net.Listener   // accepts inbound traffic for the layer
	addr   net.Addr       // advertised address backing ln
	dialer *Dialer        // creates outbound connections to peers
	logger zerolog.Logger // emits structured logs for layer activity
}

// NewLayer returns a new instance of Layer.
func NewLayer(ln net.Listener, dialer *Dialer) (*Layer, error) {
	newLogger := logger.GetLogger("layer")
	if ln == nil {
		newLogger.Error().Msg("nil listener provided to NewLayer")
		return nil, errListenerNil
	}
	if dialer == nil {
		newLogger.Error().Msg("nil dialer provided to NewLayer")
		return nil, errDialerNil
	}
	listenAddr := ln.Addr()
	if listenAddr != nil {
		newLogger.Info().
			Str("addr", listenAddr.String()).
			Msg("initializing TCP layer")
	} else {
		newLogger.Warn().Msg("initializing TCP layer with nil listener address")
	}
	return &Layer{
		ln:     ln,
		addr:   listenAddr,
		dialer: dialer,
		logger: newLogger,
	}, nil
}

// Dial creates a new network connection.
func (l *Layer) Dial(addr string, timeout time.Duration) (net.Conn, error) {
	l.logger.Debug().
		Str("target", addr).
		Dur("timeout", timeout).
		Msg("dialing remote layer")

	conn, err := l.dialer.Dial(addr, timeout)
	if err != nil {
		l.logger.Error().
			Err(err).
			Str("target", addr).
			Msg("failed to dial remote layer")
		return nil, err
	}

	l.logger.Debug().
		Str("target", addr).
		Msg("dialed remote layer successfully")

	return conn, nil
}

// Accept waits for the next connection.
func (l *Layer) Accept() (net.Conn, error) {
	if l.ln == nil {
		l.logger.Warn().Msg("accept called on nil listener")
		return nil, net.ErrClosed
	}

	addr := l.ln.Addr()
	addrStr := "<nil>"
	if addr != nil {
		addrStr = addr.String()
	}

	l.logger.Debug().
		Str("addr", addrStr).
		Msg("awaiting inbound layer connection")

	conn, err := l.ln.Accept()
	if err != nil {
		l.logger.Error().
			Err(err).
			Str("addr", addrStr).
			Msg("failed to accept inbound connection")
		return nil, err
	}

	if conn != nil {
		l.logger.Debug().
			Str("remote", conn.RemoteAddr().String()).
			Msg("accepted inbound layer connection")
	} else {
		l.logger.Warn().
			Msg("accepted nil inbound connection")
	}

	return conn, nil
}

// Close closes the layer.
func (l *Layer) Close() error {
	if l.ln == nil {
		l.logger.Warn().Msg("close called on nil listener")
		return nil
	}

	addr := l.ln.Addr()
	addrStr := "<nil>"
	if addr != nil {
		addrStr = addr.String()
	}

	l.logger.Info().
		Str("addr", addrStr).
		Msg("closing layer listener")

	if err := l.ln.Close(); err != nil {
		l.logger.Error().
			Err(err).
			Str("addr", addrStr).
			Msg("failed to close layer listener")
		return err
	}

	l.logger.Info().
		Str("addr", addrStr).
		Msg("layer listener closed")

	return nil
}

// Addr returns the local address for the layer.
func (l *Layer) Addr() net.Addr {
	if l.addr == nil {
		l.logger.Warn().Msg("addr requested but layer address is nil")
		return nil
	}

	l.logger.Debug().
		Str("addr", l.addr.String()).
		Msg("returning layer address")

	return l.addr
}

// Mux multiplexes a network connection.
type Mux struct {
	ln   net.Listener
	addr net.Addr
	m    map[byte]*listener // muxing on the byte to listener

	wg sync.WaitGroup

	// The amount of time to wait for the first header byte.
	timeout time.Duration

	// Out-of-band error logger
	// Logger *log.Logger
	logger zerolog.Logger

	tlsConfig *tls.Config
}

// NewMux returns a new instance of Mux for ln. If adv is nil,
// then the addr of ln is used.
func NewMux(ln net.Listener, adv net.Addr) (*Mux, error) {
	if ln == nil {
		return nil, errListenerNil
	}
	if adv == nil {
		return nil, errAddrNil
	}

	newLogger := logger.GetLogger("layer")
	return &Mux{
		ln:      ln,
		addr:    ln.Addr(),
		logger:  newLogger,
		timeout: DefaultTimeout,
		m:       make(map[byte]*listener),
	}, nil
}

// NewTLSMux returns a new instance of Mux for ln, and encrypts all traffic
// using TLS. If adv is nil, then the addr of ln is used. If insecure is true,
// then the server will not verify the client's certificate. If mutual is true,
// then the server will require the client to present a trusted certificate.
// func NewTLSMux(ln net.Listener, adv net.Addr, cert, key, caCert string, insecure, mutual bool) (*Mux, error) {
// 	// TODO: Implementation truncated
// 	return nil, nil
// }

// NewMutualTLSMux returns a new instance of Mux for ln, and encrypts all traffic
// using TLS. The server will also verify the client's certificate.
// func NewMutualTLSMux(ln net.Listener, adv net.Addr, cert, key, caCert string) (*Mux, error) {
// 	// TODO: Implementation truncated
// 	return nil, nil
// }

// func newTLSMux(ln net.Listener, adv net.Addr, cert, key, caCert string, mutual bool) (*Mux, error) {
// 	// TODO: Implementation truncated
// 	return nil, nil
// }

// Serve handles connections from ln and multiplexes them across registered listener.
func (mux *Mux) Serve() error {
	tl, _ := mux.ln.(*net.TCPListener) // ok if not TCP; tl may be nil
	if tl != nil {
		_ = tl.SetDeadline(time.Now().Add(2 * time.Second)) // periodic wake-ups
	}

	for {
		conn, err := mux.ln.Accept()
		if err != nil {
			// Listener intentionally closed -> graceful shutdown
			if errors.Is(err, net.ErrClosed) {
				return net.ErrClosed
			}

			// Deadline expired -> benign timeout; retry
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if tl != nil {
					_ = tl.SetDeadline(time.Now().Add(2 * time.Second)) // extend
				}
				// Keep retrying
				continue
			}
			// Legacy / generic check (also true for the above):
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				// Keep retrying
				continue
			}

			mux.wg.Wait()
			// close the connections
			for _, ln := range mux.m {
				close(ln.c)
			}
			return err
		}

		mux.wg.Add(1)
		go mux.handleConn(conn)
	}
}

// Stats returns status of the mux.
func (mux *Mux) Stats() (map[string]any, error) {
	// TODO: Implementation truncated
	return nil, nil
}

// Listen returns a Listener associated with the given header. Any connection
// accepted by mux is multiplexed based on the initial header byte.
func (mux *Mux) Listen(header byte) (net.Listener, error) {
	// TODO: Implementation truncated
	if _, ok := mux.m[header]; ok {
		mux.logger.Panic().Uint8("header", header).Msg("header already has a listener")
		return nil, errHeaderAlreadyInUse
	}

	l := &listener{
		c:    make(chan net.Conn),
		addr: mux.addr,
	}
	mux.m[header] = l
	return l, nil
}

func (mux *Mux) handleConn(conn net.Conn) {
	// TODO: Implementation truncated

	stats.Add(numConnectionsHandled, 1)
	defer mux.wg.Done()

	if err := conn.SetReadDeadline(time.Now().Add(mux.timeout)); err != nil {
		// mux.logger.
		conn.Close()
		return
	}

}

// listener is a receiver for connections received by Mux.
type listener struct {
	c    chan net.Conn
	addr net.Addr
}

// Accept waits for and returns the next connection to the listener.
func (ln *listener) Accept() (c net.Conn, err error) {
	conn, ok := <-ln.c
	if !ok {
		return nil, errConnClosed
	}

	return conn, nil
}

// Close is a no-op. The mux's listener should be closed instead.
func (ln *listener) Close() error { return nil }

// Addr always returns nil
func (ln *listener) Addr() net.Addr { return ln.addr }
