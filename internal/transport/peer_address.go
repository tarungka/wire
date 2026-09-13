package transport

import (
	"net"
	"strconv"

	"github.com/tarungka/wire/internal/protocol"
)

// sessionConfig advertises the actual bound port, including when Listen used
// port zero. A dial-only mux advertises no listening endpoint.
func (m *Mux) sessionConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := m.cfg
	cfg.LocalFeatures |= protocol.FeatureSessionDrain
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
	close(m.changed)
	m.changed = make(chan struct{})
	delete(m.sessions, sess)
	if m.nodes[sess.peerNodeID] == sess {
		delete(m.nodes, sess.peerNodeID)
	}
	for addr, cached := range m.peers {
		if cached == sess {
			delete(m.peers, addr)
		}
	}
}

// publishSession chooses the same connection at both endpoints when first dials
// cross. Call with m.mu held. Existing streams retain their session; selection
// only controls where subsequent streams are opened.
func (m *Mux) publishSession(addr string, candidate *Session) *Session {
	close(m.changed)
	m.changed = make(chan struct{})
	m.sessions[candidate] = struct{}{}
	node := candidate.peerNodeID
	selected := m.nodes[node]
	if selected == nil || selected.IsClosed() || m.sessionBefore(candidate, selected) {
		previous := selected
		selected = candidate
		m.nodes[node] = selected
		for alias, cached := range m.peers {
			if cached == previous {
				m.peers[alias] = selected
			}
		}
	}
	m.peers[addr] = selected
	if endpoint := candidate.peerListenAddress(); endpoint != "" {
		m.peers[endpoint] = selected
	}
	for sess := range m.sessions {
		if sess != selected && sess.peerNodeID == node {
			sess.beginDrain(m.cfg)
		}
	}
	return selected
}

func (m *Mux) sessionBefore(a, b *Session) bool {
	// Both peers prefer the connection initiated by the lower node ID.
	preferred := func(s *Session) bool { return s.initiator == (m.cfg.NodeID < s.peerNodeID) }
	if preferred(a) != preferred(b) {
		return preferred(a)
	}
	// Alias dials can create two connections in the same direction. The TCP
	// initiator's endpoint is shared evidence for a stable tie break.
	endpoint := func(s *Session) string {
		if s.initiator {
			return s.conn.LocalAddr().String()
		}
		return s.conn.RemoteAddr().String()
	}
	return endpoint(a) < endpoint(b)
}
