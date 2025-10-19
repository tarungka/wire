package pool

import (
	"net"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/logger"
)

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
	newLogger.Info().Int("capacity", maxConns).Msg("creating channel-based connection pool")

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
		c.logger.Warn().Msg("attempted to get connection from closed pool")
		return nil, ErrClosed
	}
	// TODO: remove this when its in alpha, unecessary computation
	open := atomic.LoadInt64(&c.nOpenConns)
	c.logger.Debug().Int64("open_connections", open).Msg("acquiring connection from pool")

	select {
	case conn := <-conns:
		if conn == nil {
			c.logger.Warn().Msg("received nil connection from pool channel")
			return nil, errConnNotDefined
		}
		c.logger.Debug().Msg("reusing idle connection from pool")
		return c.wrapConn(conn), nil
	default:
		conn, err := factory()
		if err != nil {
			c.logger.Error().Err(err).Msg("factory failed to create new connection")
			return nil, err
		}
		atomic.AddInt64(&c.nOpenConns, 1)
		c.logger.Debug().Int64("open_connections", atomic.LoadInt64(&c.nOpenConns)).Msg("dialled new connection for pool")
		return c.wrapConn(conn), nil
	}
}

// Close closes every connection in the pool.
func (c *channelPool) Close() {
	if c.conns == nil {
		c.logger.Warn().Msg("close invoked on already closed pool")
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	idle := len(c.conns)
	// TODO: remove this when its in alpha, unecessary computation
	open := atomic.LoadInt64(&c.nOpenConns)
	c.logger.Info().Int("idle", idle).Int64("open", open).Msg("closing connection pool")

	close(c.conns)

	for conn := range c.conns {
		c.logger.Debug().Msg("closing pooled connection during shutdown")
		conn.Close()
	}
	c.conns = nil
	c.factory = nil
	atomic.StoreInt64(&c.nOpenConns, 0)
	c.logger.Info().Msg("connection pool closed")
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
		c.logger.Error().Msg("refusing to return nil connection to pool")
		return errConnNotDefined
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conns == nil {
		c.logger.Warn().Msg("connection returned after pool closed; closing underlying connection")
		atomic.AddInt64(&c.nOpenConns, -1)
		return conn.Close()
	}

	select {
	// Add it back to the pool
	case c.conns <- conn:
		c.logger.Debug().Int("idle", len(c.conns)).Msg("returned connection to pool")
		return nil
	// If the pool cannot hold the connection then we close it
	default:
		c.logger.Warn().Int("capacity", cap(c.conns)).Msg("pool full; closing returned connection")
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
