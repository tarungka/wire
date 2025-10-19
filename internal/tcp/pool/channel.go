package pool

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/logger"
)

var errNotImplemented = errors.New("not implemented")

// channelPool implements the Pool interface based on buffered channels.
type channelPool struct {
	// storage for our net.Conn connections
	mu    sync.RWMutex
	conns chan net.Conn

	// net.Conn generator
	factory    ConnFactory
	nOpenConns int64
	logger     zerolog.Logger
}

// ConnFactory is a function to create new connections.
type ConnFactory func() (net.Conn, error)

// NewChannelPool returns a new pool based on buffered channels with a maximum capacity.
// During a Get(), If there is no new connection available in the pool, a new connection
// will be created via the ConnFactory() method.
func NewChannelPool(maxConns int, factory ConnFactory) (Pool, error) {
	if factory == nil {
		return nil, errFactoryNotDefined
	}
	if maxConns <= 0 {
		return nil, errInvalidPoolSize
	}
	newLogger := logger.GetLogger("channel")
	newLogger.Print("creating new channel")

	return &channelPool{
		conns:   make(chan net.Conn, maxConns),
		factory: factory,
		logger:  newLogger,
	}, nil
}

// Get implements the Pool interfaces Get() method. If there is no new
// connection available in the pool, a new connection will be created via the
// ConnFactory() method. Do not call Get() on a closed pool.
func (c *channelPool) Get() (net.Conn, error) {
	conns, factory := c.getConnsAndFactory()
	if c.conns == nil {
		return nil, ErrClosed
	}
	c.logger.Debug().Msg("Getting a new connection from the pool")

	select {
	case conn := <-conns:
		// TODO: think if I need to validate if conn can ever be nil here
		return conn, nil
	default:
		conn, err := factory()
		if err != nil {
			return nil, err
		}
		atomic.AddInt64(&c.nOpenConns, 1)
		return conn, nil
	}
}

// Close closes every connection in the pool.
func (c *channelPool) Close() {
	if c.conns == nil {
		c.logger.Warn().Msg("Close connections called when no connection pool is available")
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	close(c.conns)

	for conn := range c.conns {
		conn.Close()
	}
	c.conns = nil
	c.factory = nil
	atomic.StoreInt64(&c.nOpenConns, 0)
}

// Len returns the number of idle connections.
func (c *channelPool) Len() int {
	conn, _ := c.getConnsAndFactory()
	return len(conn)
}

// Number of open connections
func (c *channelPool) LenUsedConnections() int {
	return int(c.nOpenConns)
}

// Stats returns stats for the pool.
func (c *channelPool) Stats() (map[string]any, error) {
	conns, _ := c.getConnsAndFactory()
	return map[string]any{
		"idle":               len(conns),
		"openConnections":    c.nOpenConns,
		"maxOpenConnections": cap(conns),
	}, nil
}

// put puts the connection back to the pool. If the pool is full or closed,
// conn is simply closed. A nil conn will be rejected.
func (c *channelPool) put(conn net.Conn) error {
	if conn == nil {
		return errConnNotDefined
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conns == nil {
		atomic.StoreInt64(&c.nOpenConns, -1)
		return conn.Close()
	}

	select {
	// Add it back to the pool
	case c.conns <- conn:
		return nil
	// If the pool cannot hold the connection then we close it
	default:
		atomic.AddInt64(&c.nOpenConns, -1)
		return conn.Close()
	}
}

func (c *channelPool) getConnsAndFactory() (chan net.Conn, ConnFactory) {
	c.mu.RLock()
	defer c.mu.Unlock()
	return c.conns, c.factory
}

// wrapConn wraps a standard net.Conn to a PoolConn net.Conn.
func (c *channelPool) wrapConn(conn net.Conn) net.Conn {
	p := &PoolConn{c: c}
	p.Conn = conn
	return p
}
