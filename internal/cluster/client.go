package cluster

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/rqlite/rqlite/v8/auth"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/cluster/proto"
	command "github.com/tarungka/wire/internal/command/proto"
	"github.com/tarungka/wire/internal/tcp"
	"github.com/tarungka/wire/internal/tcp/pool"
)

const (
	maxPoolCapacity   = 64
	defaultMaxRetries = 0
	noRetries         = 0

	protoBufferLengthSize = 8
)

var errNotImplemented = errors.New("not implemented")

// CreateRaftDialer creates a dialer for connecting to other nodes' Raft service. If the cert and
// key arguments are not set, then the returned dialer will not use TLS.
func CreateRaftDialer(cert, key, caCert, serverName string, Insecure bool) (*tcp.Dialer, error) {
	return nil, errNotImplemented
}

// CredentialsFor returns a Credentials instance for the given username, or nil if
// the given CredentialsStore is nil, or the username is not found.
func CredentialsFor(credStr *auth.CredentialsStore, username string) *proto.Credentials {
	return nil
}

// Client allows communicating with a remote node.
type Client struct {
	dialer  Dialer
	timeout time.Duration

	localMu       sync.RWMutex
	localNodeAddr string
	localServ     *Service

	poolMu sync.RWMutex
	pools  map[string]pool.Pool

	// Logger
	logger zerolog.Logger
}

// NewClient returns a client instance for talking to a remote node.
// Clients will retry certain commands if they fail, to allow for
// remote node restarts. Cluster management operations such as joining
// and removing nodes are not retried, to make it clear to the operator
// that the operation failed. In addition, higher-level code will
// usually retry these operations.
func NewClient(dl Dialer, t time.Duration) *Client {
	return nil
}

// SetLocal informs the client instance of the node address for the node
// using this client. Along with the Service instance it allows this
// client to serve requests for this node locally without the network hop.
func (c *Client) SetLocal(nodeAddr string, serv *Service) error {
	return errNotImplemented
}

// GetNodeAPIAddr retrieves the API Address for the node at nodeAddr
func (c *Client) GetNodeAPIAddr(nodeAddr string, retries int, timeout time.Duration) (string, error) {
	return "", errNotImplemented
}

// GetCommitIndex retrieves the commit index for the node at nodeAddr
func (c *Client) GetCommitIndex(nodeAddr string, retries int, timeout time.Duration) (uint64, error) {
	return 0, errNotImplemented
}

// Execute performs an Execute on a remote node. If username is an empty string
// no credential information will be included in the Execute request to the
// remote node.
func (c *Client) Execute(er *command.ExecuteRequest, nodeAddr string, creds *proto.Credentials, timeout time.Duration, retries int) ([]*command.ExecuteQueryResponse, error) {
	return nil, errNotImplemented
}

// Query performs a Query on a remote node.
func (c *Client) Query(qr *command.QueryRequest, nodeAddr string, creds *proto.Credentials, timeout time.Duration) ([]*command.QueryRows, error) {
	return nil, errNotImplemented
}

// Request performs an ExecuteQuery on a remote node.
func (c *Client) Request(r *command.ExecuteQueryRequest, nodeAddr string, creds *proto.Credentials, timeout time.Duration, retries int) ([]*command.ExecuteQueryResponse, error) {
	return nil, errNotImplemented
}

// Backup retrieves a backup from a remote node and writes to the io.Writer
func (c *Client) Backup(br *command.BackupRequest, nodeAddr string, creds *proto.Credentials, timeout time.Duration, w io.Writer) error {
	return errNotImplemented
}

// Load loads a BadgerDB file into the database.
func (c *Client) Load(lr *command.LoadRequest, nodeAddr string, creds *proto.Credentials, timeout time.Duration, retries int) error {
	return errNotImplemented
}

// RemoveNode removes a node from the cluster
func (c *Client) RemoveNode(rn *command.RemoveNodeRequest, nodeAddr string, creds *proto.Credentials, timeout time.Duration) error {
	return errNotImplemented
}

// Notify notifies a remote node that this node is ready to bootstrap.
func (c *Client) Notify(nr *command.NotifyRequest, nodeAddr string, creds *proto.Credentials, timeout time.Duration) error {
	return errNotImplemented
}

// Join joins this node to a cluster at the remote address nodeAddr.
func (c *Client) Join(jr *command.JoinRequest, nodeAddr string, creds *proto.Credentials, timeout time.Duration) error {
	return errNotImplemented
}

// Stats returns stats on the Client instance
func (c *Client) Stats() (map[string]any, error) {
	return nil, errNotImplemented
}

func (c *Client) dial(nodeAddr string) (net.Conn, error) {
	return nil, errNotImplemented
}

// retry retries a command on a remote node. It does this so we churn through connections
// in the pool if we hit an error, as the remote node may have restarted and the pool's
// connections are now stale.
func (c *Client) retry(command *proto.Command, nodeAddr string, timeout time.Duration, maxRetries int) ([]byte, int, error) {
	return nil, 0, errNotImplemented
}

// writeCommand writes command to the connection.
func writeCommand(conn net.Conn, c *proto.Command, timeout time.Duration) error {
	return errNotImplemented
}

func readResponse(conn net.Conn, timeout time.Duration) (buf []byte, retErr error) {
	return nil, errNotImplemented
}

func handleConnError(conn net.Conn) {
}
