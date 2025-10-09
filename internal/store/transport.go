package store

import (
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/hashicorp/raft"
	"github.com/tarungka/wire/internal/store/gzip"
)

// Layer is the interface expected by the Store for network communication
// between nodes, which is used for Raft distributed consensus.
type Layer interface {
	net.Listener
	Dial(address string, timeout time.Duration) (net.Conn, error)
}

// Transport is the network service provided to Raft, and wraps a Listener.
type Transport struct {
	ly Layer
}

// NewTransport returns an initialized Transport.
func NewTransport(ly Layer) *Transport {
	// TODO: Implementation truncated
	return nil
}

// Dial creates a new network connection.
func (t *Transport) Dial(addr raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	// TODO: Implementation truncated
	return nil, nil
}

// Accept waits for the next connection.
func (t *Transport) Accept() (net.Conn, error) {
	// TODO: Implementation truncated
	return nil, nil
}

// Close closes the transport
func (t *Transport) Close() error {
	// TODO: Implementation truncated
	return nil
}

// Addr returns the binding address of the transport.
func (t *Transport) Addr() net.Addr {
	// TODO: Implementation truncated
	return nil

}

// NodeTransport is a wrapper around the Raft NetworkTransport, which allows
// custom configuration of the InstallSnapshot method.
type NodeTransport struct {
	*raft.NetworkTransport
	commandCommitIndex *atomic.Uint64
	leaderCommitIndex  *atomic.Uint64
	done               chan struct{}
	closed             bool
}

// NewNodeTransport returns an initialized NodeTransport.
func NewNodeTransport(transport *raft.NetworkTransport) *NodeTransport {
	// TODO: Implementation truncated
	return nil
}

// CommandCommitIndex returns the index of the latest committed log entry
// which is applied to the FSM.
func (n *NodeTransport) CommandCommitIndex() uint64 {
	// TODO: Implementation truncated
	return 0
}

// LeaderCommitIndex returns the index of the latest committed log entry
// which is known to be replicated to the majority of the cluster.
func (n *NodeTransport) LeaderCommitIndex() uint64 {
	// TODO: Implementation truncated
	return 0
}

// Close closes the transport
func (n *NodeTransport) Close() error {
	// TODO: Implementation truncated
	return nil
}

// InstallSnapshot is used to push a snapshot down to a follower. The data is read from
// the ReadCloser and streamed to the client.
func (n *NodeTransport) InstallSnapshot(id raft.ServerID, target raft.ServerAddress, args *raft.InstallSnapshotRequest,
	resp *raft.InstallSnapshotResponse, data io.Reader) error {
	gzipData, err := gzip.NewCompressor(data, gzip.DefaultBufferSize)
	if err != nil {
		return err
	}
	defer gzipData.Close()
	return n.NetworkTransport.InstallSnapshot(id, target, args, resp, gzipData)
}

// Consumer returns a channel of RPC requests to be consumed.
func (n *NodeTransport) Consumer() <-chan raft.RPC {
	// TODO: Implementation truncated
	return nil
}

// Stats returns the current stats of the transport.
func (n *NodeTransport) Stats() map[string]interface{} {
	// TODO: Implementation truncated
	return nil
}
