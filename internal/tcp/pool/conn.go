package pool

import (
	"net"
	"sync"
)

// PoolConn is a wrapper around net.Conn to modify the behavior of
// net.Conn's Close() method.
type PoolConn struct {
	net.Conn // promote net.Conn here and override the close() method
	mu       sync.Mutex
	c        *channelPool
	unusable bool
}

// Close puts the given connection back into the pool instead of closing it.
func (p *PoolConn) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.unusable {
		if p.Conn != nil {
			return p.Conn.Close()
		}
		return nil
	}

	// put it back in the pool
	return p.c.put(p.Conn)
}

// MarkUnusable marks the connection not usable anymore, to let the pool close it instead of returning it to pool.
func (p *PoolConn) MarkUnusable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unusable = true
}
