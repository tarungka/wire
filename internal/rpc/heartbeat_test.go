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
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 20 * time.Millisecond
	cfg.HeartbeatTimeout = 5 * time.Second
	cfg.MaxConsecutiveHeartbeatFailures = 5

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	var mu sync.Mutex
	callCount := 0
	failUntil := 2 // fail the first 2 calls, then succeed

	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()
		if n <= failUntil {
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

	contactLostCalled := false
	sender := &HeartbeatSender{
		client: rpcClient,
		cfg:    cfg,
		buildRequestFn: func() *HeartbeatRequest {
			return &HeartbeatRequest{WorkerID: "w-1"}
		},
		handleCommandsFn: func(cmds []WorkerCommand) {},
		onContactLost: func() {
			mu.Lock()
			contactLostCalled = true
			mu.Unlock()
		},
		metrics: NoopHeartbeatMetrics(),
		log:     testLogger(),
	}

	go sender.Run(ctx)

	// Wait for enough cycles: 2 failures + a few successes.
	time.Sleep(cfg.HeartbeatInterval * 6)

	sender.mu.Lock()
	failures := sender.consecutiveFailures
	sender.mu.Unlock()

	if failures != 0 {
		t.Errorf("expected 0 consecutive failures after success, got %d", failures)
	}

	mu.Lock()
	lost := contactLostCalled
	mu.Unlock()

	if lost {
		t.Error("callback should not have fired (only 2 failures, threshold 5)")
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
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 20 * time.Millisecond
	cfg.HeartbeatTimeout = 10 * time.Millisecond
	cfg.MaxConsecutiveHeartbeatFailures = 2

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

	// Sender with nil onContactLost — should not panic.
	sender := &HeartbeatSender{
		client: rpcClient,
		cfg:    cfg,
		buildRequestFn: func() *HeartbeatRequest {
			return &HeartbeatRequest{WorkerID: "w-1"}
		},
		handleCommandsFn: func(cmds []WorkerCommand) {},
		onContactLost:    nil,
		metrics:          NoopHeartbeatMetrics(),
		log:              testLogger(),
	}

	go sender.Run(ctx)

	// Wait enough to exceed the threshold — should not panic.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.MaxConsecutiveHeartbeatFailures+3))
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

func TestRecordHeartbeatUnknownWorker(t *testing.T) {
	cfg := DefaultConfig()
	tracker := NewHeartbeatTracker(cfg, nil)
	tracker.log = testLogger()

	// Recording a heartbeat for an unknown worker should not panic or create state.
	tracker.RecordHeartbeat("nonexistent", nil, nil, nil)

	_, ok := tracker.GetWorkerState("nonexistent")
	if ok {
		t.Error("expected unknown worker to remain absent from state")
	}

	workers := tracker.GetAllWorkers()
	if len(workers) != 0 {
		t.Errorf("expected 0 workers, got %d", len(workers))
	}
}

func TestRecordHeartbeatDeadWorkerIgnored(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.SuspectThreshold = 1
	cfg.DeadThreshold = 2

	tracker := NewHeartbeatTracker(cfg, nil)
	tracker.log = testLogger()

	tracker.RegisterWorker("w-1", "localhost:4002")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tracker.Run(ctx)

	// Wait for DEAD.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.DeadThreshold+3))

	state, _ := tracker.GetWorkerState("w-1")
	if state != WorkerDead {
		t.Fatalf("expected DEAD, got %s", state)
	}

	// Try to record a heartbeat for the dead worker.
	tracker.RecordHeartbeat("w-1", &WorkerLoad{CPUUsage: 0.1}, nil, nil)

	// Worker should still be DEAD.
	state, _ = tracker.GetWorkerState("w-1")
	if state != WorkerDead {
		t.Errorf("expected worker to remain DEAD after stale heartbeat, got %s", state)
	}
}

func TestOnContactLostFiresOnce(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 20 * time.Millisecond
	cfg.HeartbeatTimeout = 10 * time.Millisecond
	cfg.MaxConsecutiveHeartbeatFailures = 2

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}
	// No handler — all calls fail.
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
		handleCommandsFn: func(cmds []WorkerCommand) {},
		onContactLost: func() {
			mu.Lock()
			contactLostCount++
			mu.Unlock()
		},
		metrics: NoopHeartbeatMetrics(),
		log:     testLogger(),
	}

	go sender.Run(ctx)

	// Wait for many failures well past the threshold.
	time.Sleep(cfg.HeartbeatInterval * time.Duration(cfg.MaxConsecutiveHeartbeatFailures+5))

	mu.Lock()
	count := contactLostCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("expected onContactLost to fire exactly once, got %d", count)
	}
}

func TestOnContactLostRearms(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 20 * time.Millisecond
	cfg.HeartbeatTimeout = 5 * time.Second
	cfg.MaxConsecutiveHeartbeatFailures = 2

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	var mu sync.Mutex
	callCount := 0
	// Phase 1: fail 3 times (triggers contact lost once)
	// Phase 2: succeed 2 times (resets)
	// Phase 3: fail 3 more times (triggers contact lost again)
	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()
		switch {
		case n <= 3:
			return nil, NewRPCError(ErrCodeInternalError, "outage-1")
		case n <= 5:
			return &HeartbeatResponse{Accepted: true}, nil
		default:
			return nil, NewRPCError(ErrCodeInternalError, "outage-2")
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	contactLostCount := 0
	sender := &HeartbeatSender{
		client: rpcClient,
		cfg:    cfg,
		buildRequestFn: func() *HeartbeatRequest {
			return &HeartbeatRequest{WorkerID: "w-1"}
		},
		handleCommandsFn: func(cmds []WorkerCommand) {},
		onContactLost: func() {
			mu.Lock()
			contactLostCount++
			mu.Unlock()
		},
		metrics: NoopHeartbeatMetrics(),
		log:     testLogger(),
	}

	go sender.Run(ctx)

	// Wait for all phases to complete: 3+2+3 = 8 calls + buffer.
	time.Sleep(cfg.HeartbeatInterval * 12)

	mu.Lock()
	count := contactLostCount
	mu.Unlock()

	if count != 2 {
		t.Errorf("expected onContactLost to fire twice (once per outage), got %d", count)
	}
}

// Suppress unused import warning for protocol.
var _ = protocol.EncodeMsgPack
