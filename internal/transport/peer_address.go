package transport

import (
	"net"
	"strconv"
)

// sessionConfig advertises the actual bound port, including when Listen used
// port zero. A dial-only mux advertises no listening endpoint.
func (m *Mux) sessionConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := m.cfg
	if m.listener != nil {
		if addr, ok := m.listener.Addr().(*net.TCPAddr); ok {
			cfg.sessionListenPort = uint16(addr.Port)
		}
	}
	return cfg
}

// peerListenAddress uses the observed IP, never a peer-supplied hostname. The
// advertisement is a cache key only; it does not trigger an outbound connection.
func (s *Session) peerListenAddress() string {
	s.negotiationMu.Lock()
	defer s.negotiationMu.Unlock()
	if s.peerListenPort == 0 {
		return ""
	}
	host, _, err := net.SplitHostPort(s.conn.RemoteAddr().String())
	if err != nil {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(int(s.peerListenPort)))
}

func (m *Mux) forgetSession(sess *Session) {
	_ = sess.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sess)
	for addr, cached := range m.peers {
		if cached == sess {
			delete(m.peers, addr)
		}
	}
}
