package coordinator

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestSchedulerHungReservationDoesNotBlockOtherJobs(t *testing.T) {
	c, _ := newTestCoordinator(t)
	a, b := net.Pipe()
	caller, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := yamux.Server(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(rpc.DefaultConfig())
	entered := make(chan struct{}, 32)
	var calls atomic.Int32
	server.Register(rpc.MethodRequestTaskSlots, func(ctx context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var req rpc.RequestTaskSlotsRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, err.Error())
		}
		if req.Release {
			return &rpc.RequestTaskSlotsResponse{}, nil
		}
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return nil, rpc.NewRPCError(rpc.ErrCodeTimeout, ctx.Err().Error())
	})
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan struct{})
	go func() { defer close(serverDone); server.ServeSession(ctx, peer) }()
	defer func() { cancel(); _ = caller.Close(); server.Stop(); <-serverDone }()
	c.workers["hung"] = &WorkerMeta{ID: "hung", Address: "hung:1", TaskSlotsTotal: 1, TaskSlotsAvailable: 1, LastHeartbeat: time.Now(), SupportsReservations: true, RPCPeerEpoch: c.epoch, RPCClient: rpc.NewClient(caller, rpc.DefaultConfig())}
	slow := slotReleaseJob(t, "slow")
	c.jobs[slow.ID] = slow
	schedulerDone := make(chan struct{})
	go func() { defer close(schedulerDone); c.runScheduler(ctx) }()
	defer func() {
		cancel()
		select {
		case <-schedulerDone:
		case <-time.After(2 * time.Second):
			t.Error("scheduler did not join cancelled deployment")
		}
	}()
	c.kickScheduler()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("reservation not attempted")
	}
	fast := slotReleaseJob(t, "fast")
	c.mu.Lock()
	c.workers["healthy"] = &WorkerMeta{ID: "healthy", Address: "healthy:1", TaskSlotsTotal: 4, TaskSlotsAvailable: 4, LastHeartbeat: time.Now()}
	c.jobs[fast.ID] = fast
	c.mu.Unlock()
	c.kickScheduler()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.RLock()
		status := fast.Status
		c.mu.RUnlock()
		if status != JobCreated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hung reservation blocked unrelated job")
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("overlapping deployment for same job: %d", calls.Load())
	}
}
