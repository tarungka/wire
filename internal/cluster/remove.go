package cluster

import (
	"time"

	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/cluster/proto"
	command "github.com/tarungka/wire/internal/command/proto"
	"github.com/tarungka/wire/internal/logger"
)

const (
	removeRetries = 5
	removeDelay   = 250 * time.Millisecond
)

// Control is an interface for interacting with a cluster.
type Control interface {
	WaitForLeader(time.Duration) (string, error)
	WaitForRemoval(string, time.Duration) error
}

// Remover executes a node-removal operation.
type Remover struct {
	timeout time.Duration
	control Control
	client  *Client
	creds   *proto.Credentials

	// log *log.Logger
	log zerolog.Logger
}

// / NewRemover returns an instantiated Remover.
func NewRemover(client *Client, timeout time.Duration, control Control) *Remover {
	return &Remover{
		client:  client,
		timeout: timeout,
		control: control,
		// log:     log.New(os.Stderr, "[cluster-remove] ", log.LstdFlags),
		log: logger.GetLogger("cluster-remove"),
	}
}

// SetCredentials sets the credentials for the Remover.
func (r *Remover) SetCredentials(creds *proto.Credentials) {
	r.creds = creds
}

// Do executes the node-removal operation.
func (r *Remover) Do(id string, confirm bool) error {
	rn := &command.RemoveNodeRequest{
		Id: id,
	}

	nRetries := 0
	for {
		err := func() error {
			laddr, innerErr := r.control.WaitForLeader(r.timeout)
			if innerErr != nil {
				return innerErr
			}

			r.log.Printf("removing node %s from cluster via leader at %s", id, laddr)
			if innerErr = r.client.RemoveNode(rn, laddr, r.creds, r.timeout); innerErr != nil {
				r.log.Printf("failed to remove node %s from cluster via leader at %s: %s", id, laddr, innerErr)
				return innerErr
			}
			return nil
		}()
		if err == nil {
			break
		}

		nRetries++
		if nRetries == removeRetries {
			return err
		}
		time.Sleep(removeDelay)
	}

	if confirm {
		if err := r.control.WaitForRemoval(id, r.timeout); err != nil {
			return err
		}
	}

	return nil
}
