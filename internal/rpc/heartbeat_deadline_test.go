package rpc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeartbeatContactDeadlineBoundsHungRPC(t *testing.T) {
	caller, peer := testYamuxPair(t)
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.CoordinatorContactTimeout = 80 * time.Millisecond
	cfg.HeartbeatTimeout = time.Second
	srv := NewServer(cfg)
	entered := make(chan struct{}, 1)
	srv.Register(MethodHeartbeat, func(ctx context.Context, _ uint64, _ []byte) (any, *RPCError) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, NewRPCError(ErrCodeTimeout, "blocked")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ServeSession(ctx, peer)
	defer srv.Stop()
	var lost atomic.Int32
	sender := NewHeartbeatSender(NewClient(caller, cfg), cfg, func() *HeartbeatRequest {
		return &HeartbeatRequest{WorkerID: "worker", Timestamp: time.Now().Add(24 * time.Hour).UnixMilli()}
	}, nil, WithContactLostCallback(func() { lost.Add(1) }))
	started := time.Now()
	done := make(chan struct{})
	go func() { defer close(done); sender.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("RPC did not start")
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("hung RPC bypassed contact deadline")
	}
	if elapsed := time.Since(started); elapsed < cfg.CoordinatorContactTimeout {
		t.Fatalf("premature loss after %s", elapsed)
	}
	if lost.Load() != 1 {
		t.Fatalf("loss callbacks=%d", lost.Load())
	}
}

func TestHeartbeatZeroFailureLimitUsesElapsedTime(t *testing.T) {
	caller, peer := testYamuxPair(t)
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 5 * time.Millisecond
	cfg.CoordinatorContactTimeout = 100 * time.Millisecond
	srv := NewServer(cfg)
	var requests atomic.Int32
	srv.Register(MethodHeartbeat, func(context.Context, uint64, []byte) (any, *RPCError) {
		requests.Add(1)
		return &HeartbeatResponse{Accepted: false}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ServeSession(ctx, peer)
	defer srv.Stop()
	var lost atomic.Int32
	sender := NewHeartbeatSender(NewClient(caller, cfg), cfg, func() *HeartbeatRequest { return &HeartbeatRequest{} }, nil, WithContactLostCallback(func() { lost.Add(1) }))
	start := time.Now()
	sender.Run(ctx)
	if time.Since(start) < cfg.CoordinatorContactTimeout || requests.Load() < 2 || lost.Load() != 1 {
		t.Fatalf("timeout-only policy: requests=%d loss=%d", requests.Load(), lost.Load())
	}
}

func TestTrackerRejectsLateHeartbeatBeforeDetectionTick(t *testing.T) {
	cfg := DefaultConfig()
	tracker := NewHeartbeatTracker(cfg, nil)
	tracker.RegisterWorker("worker", "worker:1")
	tracker.mu.Lock()
	tracker.workers["worker"].LastHeartbeat = time.Now().Add(-2 * cfg.CoordinatorContactTimeout)
	tracker.mu.Unlock()
	tracker.RecordHeartbeat("worker", nil, nil, nil)
	state, _ := tracker.GetWorkerState("worker")
	if state != WorkerDead {
		t.Fatal("late heartbeat restored expired authority")
	}
	tracker.RegisterWorker("worker", "worker:1")
	tracker.RecordHeartbeat("worker", nil, nil, nil)
	state, _ = tracker.GetWorkerState("worker")
	if state != WorkerAlive {
		t.Fatal("new registration did not restore liveness")
	}
}

func TestHeartbeatAuthorityUsesRequestSendTime(t *testing.T) {
	caller, peer := testYamuxPair(t)
	cfg := DefaultConfig()
	srv := NewServer(cfg)
	received := make(chan time.Time, 1)
	release := make(chan struct{})
	srv.Register(MethodHeartbeat, func(ctx context.Context, _ uint64, _ []byte) (any, *RPCError) {
		received <- time.Now()
		select {
		case <-release:
		case <-ctx.Done():
			return nil, NewRPCError(ErrCodeTimeout, "cancelled")
		}
		return &HeartbeatResponse{Accepted: true, EpochID: 1}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ServeSession(ctx, peer)
	defer srv.Stop()
	var confirmed time.Time
	sender := NewHeartbeatSender(NewClient(caller, cfg), cfg, func() *HeartbeatRequest { return &HeartbeatRequest{EpochID: 1} }, nil, WithContactConfirmedCallback(func(sent time.Time) { confirmed = sent }))
	done := make(chan struct{})
	go func() { defer close(done); sender.sendHeartbeat(ctx) }()
	accepted := <-received
	close(release)
	<-done
	if confirmed.IsZero() || confirmed.After(accepted) || !sender.lastContact.Equal(confirmed) {
		t.Fatalf("reply latency extended authority: confirmed=%v received=%v", confirmed, accepted)
	}
}

func TestHeartbeatDeadlineBoundaryAndDefaults(t *testing.T) {
	deadline := time.Now()
	if err := heartbeatReplyDeadline(nil, deadline, deadline); err == nil {
		t.Fatal("reply at expiry renewed authority")
	}
	if err := heartbeatReplyDeadline(nil, deadline.Add(-time.Nanosecond), deadline); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.CoordinatorContactTimeout = 0
	var lost int
	sender := NewHeartbeatSender(nil, cfg, nil, nil, WithContactLostCallback(func() { lost++ }), WithSenderMetrics(NoopHeartbeatMetrics()))
	sender.lastContact = deadline.Add(-DefaultCoordinatorContactTimeout)
	sender.sendHeartbeat(context.Background())
	if lost != 1 {
		t.Fatal("default elapsed timeout not enforced before sending")
	}
	tracker := NewHeartbeatTracker(cfg, nil, WithHeartbeatMetrics(NoopHeartbeatMetrics()))
	tracker.RegisterWorker("worker", "worker:1")
	tracker.RecordHeartbeat("worker", nil, nil, nil)
	base := tracker.GetAllWorkers()[0].LastHeartbeat
	tracker.checkWorkersAt(base.Add(DefaultCoordinatorContactTimeout))
	if state, _ := tracker.GetWorkerState("worker"); state != WorkerDead {
		t.Fatal("default tracker deadline not enforced")
	}
}
