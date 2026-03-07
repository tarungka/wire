package coordinator

import "context"

// LeaderContext is returned when a node wins an election. The Ctx is canceled
// if leadership is lost (e.g. lease expiry, network partition).
type LeaderContext struct {
	Epoch  uint64
	Ctx    context.Context
	Cancel context.CancelFunc
}

// LeaderElection is the interface for pluggable leader election backends.
type LeaderElection interface {
	// Campaign blocks until the node becomes the leader or ctx is canceled.
	// On success it returns a LeaderContext whose Ctx is canceled if leadership
	// is lost.
	Campaign(ctx context.Context, nodeID string) (*LeaderContext, error)

	// Resign voluntarily gives up leadership.
	Resign(ctx context.Context) error

	// GetLeader returns the current leader's node ID and address.
	// Returns ErrNoLeader if no leader is elected.
	GetLeader(ctx context.Context) (nodeID string, addr string, err error)

	// Close releases resources held by the election backend.
	Close() error
}
