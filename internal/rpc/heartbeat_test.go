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
	tracker.RecordHeartbeat("w-1", &WorkerLoad{CPUUsage: 0.5})

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
		log: testLogger(),
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

// Suppress unused import warning for protocol.
var _ = protocol.EncodeMsgPack
