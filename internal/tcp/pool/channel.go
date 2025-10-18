package pool

import (
	"errors"
	"net"
	"sync"
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
}

// ConnFactory is a function to create new connections.
type ConnFactory func() (net.Conn, error)

// NewChannelPool returns a new pool based on buffered channels with a maximum capacity.
// During a Get(), If there is no new connection available in the pool, a new connection
// will be created via the ConnFactory() method.
func NewChannelPool(maxCap int, factory ConnFactory) (Pool, error) {
	return nil, errNotImplemented
}

// Get implements the Pool interfaces Get() method. If there is no new
// connection available in the pool, a new connection will be created via the
// ConnFactory() method. Do not call Get() on a closed pool.
func (c *channelPool) Get() (net.Conn, error) {
	return nil, errNotImplemented
}

// Close closes every connection in the pool.
func (c *channelPool) Close() {
	// no-op
}

// Len returns the number of idle connections.
func (c *channelPool) Len() int {
	return 0
}

// Stats returns stats for the pool.
func (c *channelPool) Stats() (map[string]any, error) {
	return nil, errNotImplemented
}

// put puts the connection back to the pool. If the pool is full or closed,
// conn is simply closed. A nil conn will be rejected.
func (c *channelPool) put(conn net.Conn) error {
	return errNotImplemented
}

func (c *channelPool) getConnsAndFactory() (chan net.Conn, ConnFactory) {
	return nil, nil
}

// wrapConn wraps a standard net.Conn to a poolConn net.Conn.
func (c *channelPool) wrapConn(conn net.Conn) net.Conn {
	return nil
}
