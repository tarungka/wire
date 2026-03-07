package coordinator

import (
	"context"
	"sync"
)

// NoopElection is a leader election backend for single-node deployments.
// It always wins the election immediately with epoch 1.
type NoopElection struct {
	mu     sync.Mutex
	nodeID string
	addr   string
	lctx   *LeaderContext
}

// NewNoopElection creates a new single-node election backend.
// The addr is the HTTP listen address of this node.
func NewNoopElection(addr string) *NoopElection {
	return &NoopElection{addr: addr}
}

func (n *NoopElection) Campaign(ctx context.Context, nodeID string) (*LeaderContext, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.nodeID = nodeID
	derived, cancel := context.WithCancel(ctx)
	n.lctx = &LeaderContext{
		Epoch:  1,
		Ctx:    derived,
		Cancel: cancel,
	}
	return n.lctx, nil
}

func (n *NoopElection) Resign(_ context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.lctx != nil {
		n.lctx.Cancel()
		n.lctx = nil
	}
	return nil
}

func (n *NoopElection) GetLeader(_ context.Context) (string, string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.nodeID == "" {
		return "", "", ErrNoLeader
	}
	return n.nodeID, n.addr, nil
}

func (n *NoopElection) Close() error {
	return n.Resign(context.Background())
}
