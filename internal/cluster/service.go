package cluster

import (
	"encoding/binary"
	"expvar"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/analytics/planner"
	aruntime "github.com/tarungka/wire/internal/analytics/runtime"
	clstrPB "github.com/tarungka/wire/internal/cluster/proto"
	commandProto "github.com/tarungka/wire/internal/command/proto"
	"github.com/tarungka/wire/internal/logger"
	pb "google.golang.org/protobuf/proto"
)

// stats captures stats for the Cluster service.
var stats *expvar.Map

const (
	numGetNodeAPIRequest  = "num_get_node_api_req"
	numGetNodeAPIResponse = "num_get_node_api_resp"
	numExecuteRequest     = "num_execute_req"
	numQueryRequest       = "num_query_req"
	numRequestRequest     = "num_request_req"
	numBackupRequest      = "num_backup_req"
	numLoadRequest        = "num_load_req"
	numRemoveNodeRequest  = "num_remove_node_req"
	numNotifyRequest      = "num_notify_req"
	numJoinRequest        = "num_join_req"

	numClientRetries            = "num_client_retries"
	numGetNodeAPIRequestRetries = "num_get_node_api_req_retries"
	numClientLoadRetries        = "num_client_load_retries"
	numClientExecuteRetries     = "num_client_execute_retries"
	numClientQueryRetries       = "num_client_query_retries"
	numClientRequestRetries     = "num_client_request_retries"
	numClientReadTimeouts       = "num_client_read_timeouts"
	numClientWriteTimeouts      = "num_client_write_timeouts"

	// Client stats for this package.
	numGetNodeAPIRequestLocal = "num_get_node_api_req_local"
)

const (
	// MuxRaftHeader is the byte used to indicate internode Raft communications.
	MuxRaftHeader = 1

	// MuxClusterHeader is the byte used to request internode cluster state information.
	MuxClusterHeader = 2 // Cluster state communications
)

func init() {
	stats = expvar.NewMap("cluster")
	stats.Add(numGetNodeAPIRequest, 0)
	stats.Add(numGetNodeAPIResponse, 0)
	stats.Add(numExecuteRequest, 0)
	stats.Add(numQueryRequest, 0)
	stats.Add(numRequestRequest, 0)
	stats.Add(numBackupRequest, 0)
	stats.Add(numLoadRequest, 0)
	stats.Add(numRemoveNodeRequest, 0)
	stats.Add(numGetNodeAPIRequestLocal, 0)
	stats.Add(numNotifyRequest, 0)
	stats.Add(numJoinRequest, 0)
	stats.Add(numClientRetries, 0)
	stats.Add(numGetNodeAPIRequestRetries, 0)
	stats.Add(numClientLoadRetries, 0)
	stats.Add(numClientExecuteRetries, 0)
	stats.Add(numClientQueryRetries, 0)
	stats.Add(numClientRequestRetries, 0)
	stats.Add(numClientReadTimeouts, 0)
	stats.Add(numClientWriteTimeouts, 0)
}

// Dialer is the interface dialers must implement.
type Dialer interface {
	// Dial is used to create a connection to a service listening
	// on an address.
	Dial(address string, timeout time.Duration) (net.Conn, error)
}

// Database is the interface any queryable system must implement
type Database interface {
}

// Manager is the interface any cluster management system must implement.
type Manager interface {
	// Join joins a node to the cluster.
	Join(jr *commandProto.JoinRequest) error

	// Notify notifies a node that another node is ready.
	Notify(nr *commandProto.NotifyRequest) error

	// Remove removes a node from the cluster.
	Remove(rn *commandProto.RemoveNodeRequest) error

	// LeaderAddr returns the Raft address of the leader.
	LeaderAddr() (string, error)

	// CommitIndex returns the Raft commit index.
	CommitIndex() (uint64, error)
}

// Service provides cluster management and distributed database operations.
type Service struct {
	ln   net.Listener
	db   Database
	mgr  Manager
	addr string // API address

	https bool

	WorkerManager *aruntime.WorkerManager

	logger zerolog.Logger
}

// New returns a new Service instance.
func New(ln net.Listener, db Database, mgr Manager) *Service {
	return &Service{
		ln:     ln,
		db:     db,
		mgr:    mgr,
		logger: logger.GetLogger("cluster"),
	}
}

// Open opens the service.
func (s *Service) Open() error {
	go s.serve()
	return nil
}

// Close closes the service.
func (s *Service) Close() error {
	return s.ln.Close()
}

// SetAPIAddr sets the API address of this node.
func (s *Service) SetAPIAddr(addr string) {
	s.addr = addr
}

// EnableHTTPS enables HTTPS for the API.
func (s *Service) EnableHTTPS(https bool) {
	s.https = https
}

// GetNodeAPIURL returns the API URL for this node.
func (s *Service) GetNodeAPIURL() string {
	protocol := "http"
	if s.https {
		protocol = "https"
	}
	return fmt.Sprintf("%s://%s", protocol, s.addr)
}

func (s *Service) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Service) handleConn(conn net.Conn) {
	defer conn.Close()

	for {
		// Read command length
		b := make([]byte, 8)
		_, err := io.ReadFull(conn, b)
		if err != nil {
			return
		}
		sz := binary.LittleEndian.Uint64(b)

		// Read command
		p := make([]byte, sz)
		_, err = io.ReadFull(conn, p)
		if err != nil {
			return
		}

		c := &clstrPB.Command{}
		err = pb.Unmarshal(p, c)
		if err != nil {
			return
		}

		switch c.Type {
		case clstrPB.Command_COMMAND_TYPE_GET_NODE_API_URL:
			s.logger.Print("got a command to get node api url")
			stats.Add(numGetNodeAPIRequest, 1)
			ci, err := s.mgr.CommitIndex()
			if err != nil {
				return
			}
			p, err = pb.Marshal(&clstrPB.NodeMeta{
				Url:         s.GetNodeAPIURL(),
				CommitIndex: ci,
			})
			if err != nil {
				return
			}
			if err := writeBytesWithLength(conn, p); err != nil {
				return
			}
			stats.Add(numGetNodeAPIResponse, 1)

		case clstrPB.Command_COMMAND_TYPE_REMOVE_NODE:
			s.logger.Print("got a command to remove a node")
			stats.Add(numRemoveNodeRequest, 1)
			resp := &clstrPB.CommandRemoveNodeResponse{}

			rn := c.GetRemoveNodeRequest()
			if rn == nil {
				resp.Error = "LoadRequest is nil"
			} else {
				if err := s.mgr.Remove(rn); err != nil {
					resp.Error = err.Error()
				}
			}
			if err := marshalAndWrite(conn, resp); err != nil {
				return
			}

		case clstrPB.Command_COMMAND_TYPE_NOTIFY:
			s.logger.Print("got a command to notify")
			stats.Add(numNotifyRequest, 1)
			resp := &clstrPB.CommandNotifyResponse{}

			nr := c.GetNotifyRequest()
			if nr == nil {
				resp.Error = "NotifyRequest is nil"
			} else {
				if err := s.mgr.Notify(nr); err != nil {
					resp.Error = err.Error()
				}
			}
			if err := marshalAndWrite(conn, resp); err != nil {
				return
			}

		case clstrPB.Command_COMMAND_TYPE_JOIN:
			s.logger.Print("got a command to join")
			stats.Add(numJoinRequest, 1)
			resp := &clstrPB.CommandJoinResponse{}

			jr := c.GetJoinRequest()
			if jr == nil {
				resp.Error = "JoinRequest is nil"
			} else {
				if jr.Voter {
					if err := s.mgr.Join(jr); err != nil {
						resp.Error = err.Error()
						if err.Error() == "not leader" {
							laddr, err := s.mgr.LeaderAddr()
							if err != nil {
								resp.Error = err.Error()
							} else {
								resp.Leader = laddr
							}
						}
					}
				} else {
					resp.Error = "unauthorized"
				}
			}
			if err := marshalAndWrite(conn, resp); err != nil {
				return
			}

		case clstrPB.Command_COMMAND_TYPE_ANALYTICS_DEPLOY:
			s.logger.Print("got a command to deploy analytics task")
			s.handleAnalyticsDeploy(conn, c)

		default:
		}
	}
}

func (s *Service) handleAnalyticsDeploy(conn net.Conn, c *clstrPB.Command) {
	req := c.GetAnalyticsDeployRequest()
	if req == nil {
		return
	}

	if s.WorkerManager == nil {
		s.logger.Error().Msg("WorkerManager not initialized")
		return
	}

	// Create task from request
	task := &planner.Task{
		ID:          req.TaskId,
		OperatorID:  req.JobId,
		NodeID:      s.addr,
		InputTasks:  req.InputTasks,
		OutputTasks: req.OutputTasks,
	}

	// Instantiate operator
	op, err := aruntime.CreateOperator(req.OperatorType, nil)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to create operator")
		return
	}

	if err := s.WorkerManager.DeployTask(task, op); err != nil {
		s.logger.Error().Err(err).Msg("Failed to deploy task")
	}
}

func marshalAndWrite(conn net.Conn, m pb.Message) error {
	p, err := pb.Marshal(m)
	if err != nil {
		return err
	}
	return writeBytesWithLength(conn, p)
}

func writeBytesWithLength(conn net.Conn, p []byte) error {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b[0:], uint64(len(p)))
	_, err := conn.Write(b)
	return err
}
