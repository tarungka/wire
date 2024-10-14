package cluster

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/cluster/proto"
	command "github.com/tarungka/wire/internal/command/proto"
	"github.com/tarungka/wire/internal/logger"
)

var (
	// ErrNodeIDRequired is returned a join request doesn't supply a node ID
	ErrNodeIDRequired = errors.New("node required")

	// ErrJoinFailed is returned when a node fails to join a cluster
	ErrJoinFailed = errors.New("failed to join cluster")

	// ErrJoinCanceled is returned when a join operation is canceled
	ErrJoinCanceled = errors.New("join operation canceled")

	// ErrNotifyFailed is returned when a node fails to notify another node
	ErrNotifyFailed = errors.New("failed to notify node")
)

// Joiner executes a node-join operation.
type Joiner struct {
	numAttempts     int
	attemptInterval time.Duration

	client *Client
	creds  *proto.Credentials
	// logger *log.Logger
	logger zerolog.Logger
}

// NewJoiner returns an instantiated Joiner.
func NewJoiner(client *Client, numAttempts int, attemptInterval time.Duration) *Joiner {
	newLogger := logger.GetLogger("cluster-join")
	newLogger.Printf("creating a new joiner with client: %v", client)
	return &Joiner{
		client:          client,
		numAttempts:     numAttempts,
		attemptInterval: attemptInterval,
		// logger:          log.New(os.Stderr, "[cluster-join] ", log.LstdFlags),
		logger: newLogger,
	}
}

// SetCredentials sets the credentials for the Joiner.
func (j *Joiner) SetCredentials(creds *proto.Credentials) {
	j.creds = creds
}

// Do makes the actual join request. If the join is successful with any address,
// that address is returned. Otherwise, an error is returned.
func (j *Joiner) Do(ctx context.Context, targetAddrs []string, id, addr string, suf Suffrage) (string, error) {
	if id == "" {
		log.Debug().Err(ErrNodeIDRequired).Msgf("missing node id")
		return "", ErrNodeIDRequired
	}

	j.logger.Printf("attempting to join node %s:%s with %v", id, addr, targetAddrs)
	var err error
	var joinee string
	for i := 0; i < j.numAttempts; i++ {
		for _, ta := range targetAddrs {
			select {
			case <-ctx.Done():
				j.logger.Error().Err(err).Msg("error context closed, join cancelled")
				return "", ErrJoinCanceled
			default:
				joinee, err = j.join(ta, id, addr, suf)
				if err == nil {
					return joinee, nil
				}
				j.logger.Printf("failed to join via node at %s: %s", ta, err)
			}
		}
		if i+1 < j.numAttempts {
			// This logic message only make sense if performing more than 1 join-attempt.
			j.logger.Printf("failed to join cluster at %s, sleeping %s before retry", targetAddrs, j.attemptInterval)
			select {
			case <-ctx.Done():
				return "", ErrJoinCanceled
			case <-time.After(j.attemptInterval):
				continue // Proceed with the next attempt
			}
		}
	}
	j.logger.Printf("failed to join cluster at %s, after %d attempt(s)", targetAddrs, j.numAttempts)
	return "", ErrJoinFailed
}

func (j *Joiner) join(targetAddr, id, addr string, suf Suffrage) (string, error) {
	j.logger.Printf("joining %s:%s with %s", id, addr, targetAddr)
	req := &command.JoinRequest{
		Id:      id,
		Address: addr,
		Voter:   suf.IsVoter(),
	}

	// Attempt to join.
	if err := j.client.Join(req, targetAddr, j.creds, time.Second); err != nil {
		return "", err
	}
	return targetAddr, nil
}
