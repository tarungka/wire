package worker

import (
	"context"
	"io"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

// Each upload owns its connection. A failed peer/session cannot poison future
// checkpoints, and concurrent uploads do not close each other's connections.
type reconnectingCheckpointClient struct{ address string }

func (c *reconnectingCheckpointClient) ReplicateCheckpoint(ctx context.Context, request rpc.ReplicateCheckpointRequest, body io.Reader) error {
	session, err := transport.NewClientSessionContext(ctx, c.address, transport.DefaultConfig())
	if err != nil {
		return err
	}
	defer session.Close()
	return rpc.NewClient(session.YamuxSession(), rpc.DefaultConfig()).ReplicateCheckpoint(ctx, request, body)
}
