package coordinator

import (
	"context"
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

// Reservations are obtained before publishing the deployment. Legacy workers
// retain push deployment; capability-bearing workers must have a live RPC peer.
func (c *Coordinator) reserveDeployment(jobID, attempt string, assignments map[string][]rpc.TaskDescriptor) (map[string]*rpc.Client, func(), error) {
	peers := make(map[string]*rpc.Client)
	c.mu.RLock()
	epoch := c.epoch
	for id := range assignments {
		worker := c.workers[id]
		if worker == nil {
			c.mu.RUnlock()
			return nil, func() {}, fmt.Errorf("worker disappeared")
		}
		if worker.SupportsReservations {
			if worker.RPCClient == nil {
				c.mu.RUnlock()
				return nil, func() {}, fmt.Errorf("worker reverse RPC unavailable")
			}
			peers[id] = worker.RPCClient
		}
	}
	c.mu.RUnlock()
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for _, peer := range peers {
			_, _ = peer.RequestTaskSlots(ctx, &rpc.RequestTaskSlotsRequest{JobID: jobID, EpochID: epoch, ReservationID: attempt, Release: true})
		}
	}
	// One shared deadline ensures a large placement cannot outlive the lease
	// while reserving workers sequentially. Failed partial reservations expire
	// even if a release reply is lost.
	ctx, cancel := context.WithTimeout(context.Background(), rpc.DefaultRequestTaskSlotsTimeout)
	defer cancel()
	for id, peer := range peers {
		resp, err := peer.RequestTaskSlots(ctx, &rpc.RequestTaskSlotsRequest{JobID: jobID, EpochID: epoch, ReservationID: attempt, RequiredSlots: int32(len(assignments[id]))})
		if err != nil {
			release()
			return nil, func() {}, err
		}
		if resp.Granted != int32(len(assignments[id])) || resp.ReservationID != attempt {
			release()
			return nil, func() {}, fmt.Errorf("worker did not grant reservation")
		}
	}
	return peers, release, nil
}
