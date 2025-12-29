package cluster

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/rqlite/rqlite/v8/auth"
	"github.com/rqlite/rqlite/v8/rtls"
	"github.com/rs/zerolog"
	clstrPB "github.com/tarungka/wire/internal/cluster/proto"
	command "github.com/tarungka/wire/internal/command/proto"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/tcp"
	"github.com/tarungka/wire/internal/tcp/pool"
	pb "google.golang.org/protobuf/proto"
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
	var tlsConfig *tls.Config
	var err error
	if cert != "" || key != "" || caCert != "" {
		tlsConfig, err = rtls.CreateClientConfig(cert, key, caCert, serverName, Insecure)
		if err != nil {
			return nil, err
		}
	}
	return tcp.NewDialer(MuxRaftHeader, tlsConfig), nil
}

// CredentialsFor returns a Credentials instance for the given username, or nil if
// the given CredentialsStore is nil, or the username is not found.
func CredentialsFor(credStr *auth.CredentialsStore, username string) *clstrPB.Credentials {
	if credStr == nil {
		return nil
	}
	password, ok := credStr.Password(username)
	if !ok {
		return nil
	}
	return &clstrPB.Credentials{
		Username: username,
		Password: password,
	}
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
func NewClient(dl Dialer, t time.Duration) *Client {
	return &Client{
		dialer:  dl,
		timeout: t,
		pools:   make(map[string]pool.Pool),
		logger:  logger.GetLogger("cluster-client"),
	}
}

// SetLocal informs the client instance of the node address for the node
// using this client. Along with the Service instance it allows this
// client to serve requests for this node locally without the network hop.
func (c *Client) SetLocal(nodeAddr string, serv *Service) error {
	c.localMu.Lock()
	defer c.localMu.Unlock()
	c.localNodeAddr = nodeAddr
	c.localServ = serv
	return nil
}

// Join joins this node to a cluster at the remote address nodeAddr.
func (c *Client) Join(jr *command.JoinRequest, nodeAddr string, creds *clstrPB.Credentials, timeout time.Duration) error {
	cmd := &clstrPB.Command{
		Type: clstrPB.Command_COMMAND_TYPE_JOIN,
		Request: &clstrPB.Command_JoinRequest{
			JoinRequest: jr,
		},
		Credentials: creds,
	}

	_, _, err := c.retry(cmd, nodeAddr, timeout, noRetries)
	return err
}

// GetNodeAPIAddr retrieves the API Address for the node at nodeAddr
func (c *Client) GetNodeAPIAddr(nodeAddr string, retries int, timeout time.Duration) (string, error) {
	cmd := &clstrPB.Command{
		Type: clstrPB.Command_COMMAND_TYPE_GET_NODE_API_URL,
	}
	p, _, err := c.retry(cmd, nodeAddr, timeout, retries)
	if err != nil {
		return "", err
	}

	meta := &clstrPB.NodeMeta{}
	if err := pb.Unmarshal(p, meta); err != nil {
		return "", err
	}
	return meta.Url, nil
}

// retry retries a command on a remote node.
func (c *Client) retry(command *clstrPB.Command, nodeAddr string, timeout time.Duration, maxRetries int) ([]byte, int, error) {
	for i := 0; i <= maxRetries; i++ {
		conn, err := c.getConn(nodeAddr)
		if err != nil {
			return nil, i, err
		}

		if err := writeCommand(conn, command, timeout); err != nil {
			conn.Close()
			continue
		}

		p, err := readResponse(conn, timeout)
		if err != nil {
			conn.Close()
			continue
		}

		conn.Close() // Return to pool if it was a pool conn
		return p, i, nil
	}
	return nil, maxRetries, errors.New("max retries exceeded")
}

func (c *Client) getConn(nodeAddr string) (net.Conn, error) {
	c.poolMu.Lock()
	p, ok := c.pools[nodeAddr]
	if !ok {
		var err error
		p, err = pool.NewChannelPool(maxPoolCapacity, func() (net.Conn, error) {
			return c.dialer.Dial(nodeAddr, c.timeout)
		})
		if err != nil {
			c.poolMu.Unlock()
			return nil, err
		}
		c.pools[nodeAddr] = p
	}
	c.poolMu.Unlock()
	return p.Get()
}

func writeCommand(conn net.Conn, c *clstrPB.Command, timeout time.Duration) error {
	p, err := pb.Marshal(c)
	if err != nil {
		return err
	}

	if timeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(timeout))
	}

	b := make([]byte, protoBufferLengthSize)
	binary.LittleEndian.PutUint64(b, uint64(len(p)))
	if _, err := conn.Write(b); err != nil {
		return err
	}
	_, err = conn.Write(p)
	return err
}

func readResponse(conn net.Conn, timeout time.Duration) ([]byte, error) {
	if timeout > 0 {
		conn.SetReadDeadline(time.Now().Add(timeout))
	}

	b := make([]byte, protoBufferLengthSize)
	if _, err := io.ReadFull(conn, b); err != nil {
		return nil, err
	}
	sz := binary.LittleEndian.Uint64(b)

	p := make([]byte, sz)
	if _, err := io.ReadFull(conn, p); err != nil {
		return nil, err
	}
	return p, nil
}

// Stats returns stats on the Client instance
func (c *Client) Stats() (map[string]interface{}, error) {
	return nil, nil
}

// Unimplemented stubs to satisfy compiler for now if they are called elsewhere
func (c *Client) Execute(er *command.ExecuteRequest, nodeAddr string, creds *clstrPB.Credentials, timeout time.Duration, retries int) ([]*command.ExecuteQueryResponse, error) {
	return nil, errNotImplemented
}
func (c *Client) Query(qr *command.QueryRequest, nodeAddr string, creds *clstrPB.Credentials, timeout time.Duration) ([]*command.QueryRows, error) {
	return nil, errNotImplemented
}
func (c *Client) Request(r *command.ExecuteQueryRequest, nodeAddr string, creds *clstrPB.Credentials, timeout time.Duration, retries int) ([]*command.ExecuteQueryResponse, error) {
	return nil, errNotImplemented
}
func (c *Client) Notify(nr *command.NotifyRequest, nodeAddr string, creds *clstrPB.Credentials, timeout time.Duration) error {
	return errNotImplemented
}
func (c *Client) RemoveNode(rn *command.RemoveNodeRequest, nodeAddr string, creds *clstrPB.Credentials, timeout time.Duration) error {
	return errNotImplemented
}
