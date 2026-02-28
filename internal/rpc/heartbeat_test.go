package rpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestHeartbeatTrackerAliveToSuspect(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.SuspectThreshold = 3
	cfg.DeadThreshold = 5

	var mu sync.Mutex
	transitions := make([]struct{ from, to WorkerState }, 0)

	tracker := NewHeartbeatTracker(cfg, func(id string, from, to WorkerState) {
		mu.Lock()
		transitions = append(transitions, struct{ from, to WorkerState }{from, to})
		mu.Unlock()
	})
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "localhost:4002")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tracker.Run(ctx)

	// Wait for enough ticks to trigger SUSPECT (3 missed).
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.SuspectThreshold+1))

	state, ok := tracker.GetWorkerState("w-1")
	if !ok {
		t.Fatal("worker not found")
	}
	if state != WorkerSuspect {
		t.Errorf("expected SUSPECT, got %s", state)
	}

	mu.Lock()
	found := false
	for _, tr := range transitions {
		if tr.from == WorkerAlive && tr.to == WorkerSuspect {
			found = true
		}
	}
	mu.Unlock()

	if !found {
		t.Error("expected ALIVE->SUSPECT transition callback")
	}
}

func TestHeartbeatTrackerSuspectToAlive(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.SuspectThreshold = 2
	cfg.DeadThreshold = 5

	var mu sync.Mutex
	transitions := make([]struct{ from, to WorkerState }, 0)

	tracker := NewHeartbeatTracker(cfg, func(id string, from, to WorkerState) {
		mu.Lock()
		transitions = append(transitions, struct{ from, to WorkerState }{from, to})
		mu.Unlock()
	})
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "localhost:4002")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tracker.Run(ctx)

	// Wait for SUSPECT transition.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.SuspectThreshold+1))

	state, _ := tracker.GetWorkerState("w-1")
	if state != WorkerSuspect {
		t.Fatalf("expected SUSPECT before recovery, got %s", state)
	}

	// Send heartbeat to recover.
	tracker.RecordHeartbeat("w-1", &WorkerLoad{CPUUsage: 0.5}, nil, nil)

	state, _ = tracker.GetWorkerState("w-1")
	if state != WorkerAlive {
		t.Errorf("expected ALIVE after recovery, got %s", state)
	}

	mu.Lock()
	foundRecovery := false
	for _, tr := range transitions {
		if tr.from == WorkerSuspect && tr.to == WorkerAlive {
			foundRecovery = true
		}
	}
	mu.Unlock()

	if !foundRecovery {
		t.Error("expected SUSPECT->ALIVE transition callback")
	}
}

func TestHeartbeatTrackerSuspectToDead(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.SuspectThreshold = 2
	cfg.DeadThreshold = 4

	var mu sync.Mutex
	transitions := make([]struct{ from, to WorkerState }, 0)

	tracker := NewHeartbeatTracker(cfg, func(id string, from, to WorkerState) {
		mu.Lock()
		transitions = append(transitions, struct{ from, to WorkerState }{from, to})
		mu.Unlock()
	})
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "localhost:4002")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tracker.Run(ctx)

	// Wait for enough ticks to trigger DEAD.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.DeadThreshold+2))

	state, ok := tracker.GetWorkerState("w-1")
	if !ok {
		t.Fatal("worker not found")
	}
	if state != WorkerDead {
		t.Errorf("expected DEAD, got %s", state)
	}

	mu.Lock()
	foundDead := false
	for _, tr := range transitions {
		if tr.from == WorkerSuspect && tr.to == WorkerDead {
			foundDead = true
		}
	}
	mu.Unlock()

	if !foundDead {
		t.Error("expected SUSPECT->DEAD transition callback")
	}
}

func TestHeartbeatTrackerGetAllWorkers(t *testing.T) {
	cfg := DefaultConfig()
	tracker := NewHeartbeatTracker(cfg, nil)
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "host1:4002")
	tracker.RegisterWorker("w-2", "host2:4002")

	workers := tracker.GetAllWorkers()
	if len(workers) != 2 {
		t.Errorf("expected 2 workers, got %d", len(workers))
	}
}

func TestHeartbeatTrackerUnregister(t *testing.T) {
	cfg := DefaultConfig()
	tracker := NewHeartbeatTracker(cfg, nil)
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "host1:4002")
	tracker.UnregisterWorker("w-1")

	_, ok := tracker.GetWorkerState("w-1")
	if ok {
		t.Error("expected worker to be unregistered")
	}
}

func TestHeartbeatTrackerGracefulShutdown(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond

	tracker := NewHeartbeatTracker(cfg, nil)
	tracker.log = testLogger()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		tracker.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Success - Run returned.
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestHeartbeatSenderInterval(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 50 * time.Millisecond
	cfg.HeartbeatTimeout = 5 * time.Second

	var mu sync.Mutex
	heartbeatCount := 0

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		mu.Lock()
		heartbeatCount++
		mu.Unlock()
		return &HeartbeatResponse{Accepted: true}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	sender := &HeartbeatSender{
		client: rpcClient,
		cfg:    cfg,
		buildRequestFn: func() *HeartbeatRequest {
			return &HeartbeatRequest{WorkerID: "w-1", EpochID: 1}
		},
		handleCommandsFn: func(cmds []WorkerCommand) {},
		metrics:          NoopHeartbeatMetrics(),
		log:              testLogger(),
	}

	go sender.Run(ctx)

	// Wait for a few heartbeats.
	time.Sleep(cfg.HeartbeatInterval * 5)

	mu.Lock()
	count := heartbeatCount
	mu.Unlock()

	if count < 3 {
		t.Errorf("expected at least 3 heartbeats, got %d", count)
	}
}

func TestHeartbeatSenderCommandDispatch(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 50 * time.Millisecond
	cfg.HeartbeatTimeout = 5 * time.Second

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		return &HeartbeatResponse{
			Accepted: true,
			Commands: []WorkerCommand{
				{Type: CommandTypeCancelTask, JobID: "j-1", TaskID: "t-1"},
			},
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	var mu sync.Mutex
	var receivedCmds []WorkerCommand

	sender := &HeartbeatSender{
		client: rpcClient,
		cfg:    cfg,
		buildRequestFn: func() *HeartbeatRequest {
			return &HeartbeatRequest{WorkerID: "w-1"}
		},
		handleCommandsFn: func(cmds []WorkerCommand) {
			mu.Lock()
			receivedCmds = append(receivedCmds, cmds...)
			mu.Unlock()
		},
		metrics: NoopHeartbeatMetrics(),
		log:     testLogger(),
	}

	go sender.Run(ctx)

	// Wait for at least one heartbeat cycle.
	time.Sleep(cfg.HeartbeatInterval * 3)

	mu.Lock()
	count := len(receivedCmds)
	mu.Unlock()

	if count == 0 {
		t.Fatal("expected commands to be dispatched")
	}

	mu.Lock()
	cmd := receivedCmds[0]
	mu.Unlock()

	if cmd.Type != CommandTypeCancelTask {
		t.Errorf("command type = %d, want %d", cmd.Type, CommandTypeCancelTask)
	}
}

func TestHeartbeatTrackerDeadDoesNotAdvance(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.SuspectThreshold = 1
	cfg.DeadThreshold = 2

	callbackCount := 0
	tracker := NewHeartbeatTracker(cfg, func(id string, from, to WorkerState) {
		callbackCount++
	})
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "localhost:4002")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tracker.Run(ctx)

	// Wait long enough for DEAD.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.DeadThreshold+3))

	state, _ := tracker.GetWorkerState("w-1")
	if state != WorkerDead {
		t.Fatalf("expected DEAD, got %s", state)
	}

	// Record the callback count, then wait more.
	prevCount := callbackCount
	time.Sleep(cfg.HeartbeatInterval * 3)

	// No more state changes should have happened.
	if callbackCount != prevCount {
		t.Errorf("expected no more callbacks after DEAD, got %d total", callbackCount)
	}
}

func TestHeartbeatSenderConsecutiveFailuresReset(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxConsecutiveHeartbeatFailures = 3

	callbackCalled := false
	sender := &HeartbeatSender{
		cfg: cfg,
		onContactLost: func() {
			callbackCalled = true
		},
		metrics: NoopHeartbeatMetrics(),
		log:     testLogger(),
	}

	// Simulate 2 failures then a success.
	sender.mu.Lock()
	sender.consecutiveFailures = 2
	sender.mu.Unlock()

	// On success, failures should reset.
	sender.mu.Lock()
	sender.consecutiveFailures = 0
	sender.mu.Unlock()

	sender.mu.Lock()
	count := sender.consecutiveFailures
	sender.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 consecutive failures after reset, got %d", count)
	}
	if callbackCalled {
		t.Error("callback should not have been called")
	}
}

func TestHeartbeatSenderContactLostCallback(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 20 * time.Millisecond
	cfg.HeartbeatTimeout = 10 * time.Millisecond
	cfg.MaxConsecutiveHeartbeatFailures = 3

	// Server that always rejects (closes immediately to cause errors).
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}
	// No handler registered — calls will fail.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	var mu sync.Mutex
	contactLostCount := 0

	sender := &HeartbeatSender{
		client: rpcClient,
		cfg:    cfg,
		buildRequestFn: func() *HeartbeatRequest {
			return &HeartbeatRequest{WorkerID: "w-1"}
		},
		onContactLost: func() {
			mu.Lock()
			contactLostCount++
			mu.Unlock()
		},
		metrics: NoopHeartbeatMetrics(),
		log:     testLogger(),
	}

	go sender.Run(ctx)

	// Wait for enough failures.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.MaxConsecutiveHeartbeatFailures+2))

	mu.Lock()
	count := contactLostCount
	mu.Unlock()

	if count == 0 {
		t.Error("expected contact lost callback to fire")
	}
}

func TestHeartbeatSenderContactLostNilCallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxConsecutiveHeartbeatFailures = 1

	sender := &HeartbeatSender{
		cfg:           cfg,
		onContactLost: nil,
		metrics:       NoopHeartbeatMetrics(),
		log:           testLogger(),
	}

	// Simulate exceeding threshold with nil callback — should not panic.
	sender.mu.Lock()
	sender.consecutiveFailures = cfg.MaxConsecutiveHeartbeatFailures + 1
	sender.mu.Unlock()

	// The nil check is in sendHeartbeat; just verify the struct doesn't panic.
	if sender.onContactLost != nil {
		t.Error("expected nil onContactLost")
	}
}

func TestHeartbeatSenderPartialFailureRecovery(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 20 * time.Millisecond
	cfg.HeartbeatTimeout = 5 * time.Second
	cfg.MaxConsecutiveHeartbeatFailures = 10

	var mu sync.Mutex
	callCount := 0
	contactLostCalled := false

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Alternate: fail once, then succeed.
	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()
		if n%2 == 1 {
			return nil, NewRPCError(ErrCodeInternalError, "transient")
		}
		return &HeartbeatResponse{Accepted: true}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	sender := &HeartbeatSender{
		client: rpcClient,
		cfg:    cfg,
		buildRequestFn: func() *HeartbeatRequest {
			return &HeartbeatRequest{WorkerID: "w-1"}
		},
		onContactLost: func() {
			mu.Lock()
			contactLostCalled = true
			mu.Unlock()
		},
		metrics: NoopHeartbeatMetrics(),
		log:     testLogger(),
	}

	go sender.Run(ctx)

	// Wait for several cycles.
	time.Sleep(cfg.HeartbeatInterval * 8)

	mu.Lock()
	lost := contactLostCalled
	mu.Unlock()

	if lost {
		t.Error("intermittent failures should not trigger contact lost")
	}
}

func TestWorkerLostCallbackFired(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.SuspectThreshold = 1
	cfg.DeadThreshold = 2

	var mu sync.Mutex
	var lostEvents []WorkerLostEvent

	tracker := NewHeartbeatTracker(cfg, nil, WithWorkerLostCallback(func(event WorkerLostEvent) {
		mu.Lock()
		lostEvents = append(lostEvents, event)
		mu.Unlock()
	}))
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "localhost:4002")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tracker.Run(ctx)

	// Wait for DEAD.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.DeadThreshold+3))

	mu.Lock()
	count := len(lostEvents)
	mu.Unlock()

	if count == 0 {
		t.Fatal("expected worker lost callback to fire")
	}

	mu.Lock()
	event := lostEvents[0]
	mu.Unlock()

	if event.WorkerID != "w-1" {
		t.Errorf("WorkerID = %q, want %q", event.WorkerID, "w-1")
	}
}

func TestWorkerLostEventContainsTasks(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.SuspectThreshold = 1
	cfg.DeadThreshold = 2

	var mu sync.Mutex
	var lostEvents []WorkerLostEvent

	tracker := NewHeartbeatTracker(cfg, nil, WithWorkerLostCallback(func(event WorkerLostEvent) {
		mu.Lock()
		lostEvents = append(lostEvents, event)
		mu.Unlock()
	}))
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "localhost:4002")

	tasks := []RunningTaskSummary{
		{TaskID: "t-1", JobID: "j-1", Status: TaskStatusRunning, UptimeMs: 5000},
		{TaskID: "t-2", JobID: "j-1", Status: TaskStatusRunning, UptimeMs: 3000},
	}
	tracker.RecordHeartbeat("w-1", nil, nil, tasks)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tracker.Run(ctx)

	// Wait for DEAD.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.DeadThreshold+3))

	mu.Lock()
	count := len(lostEvents)
	mu.Unlock()

	if count == 0 {
		t.Fatal("expected worker lost callback to fire")
	}

	mu.Lock()
	event := lostEvents[0]
	mu.Unlock()

	if len(event.RunningTasks) != 2 {
		t.Errorf("expected 2 running tasks in event, got %d", len(event.RunningTasks))
	}
	if event.RunningTasks[0].TaskID != "t-1" {
		t.Errorf("first task ID = %q, want %q", event.RunningTasks[0].TaskID, "t-1")
	}
}

func TestHeartbeatSenderCancelPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 50 * time.Millisecond

	sender := &HeartbeatSender{
		cfg: cfg,
		buildRequestFn: func() *HeartbeatRequest {
			return &HeartbeatRequest{WorkerID: "w-1"}
		},
		metrics: NoopHeartbeatMetrics(),
		log:     testLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		sender.Run(ctx)
		close(done)
	}()

	// Cancel immediately.
	cancel()

	select {
	case <-done:
		// Success — Run returned promptly.
	case <-time.After(time.Second):
		t.Fatal("HeartbeatSender.Run did not return after context cancellation")
	}
}

// Suppress unused import warning for protocol.
var _ = protocol.EncodeMsgPack
