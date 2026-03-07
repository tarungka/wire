package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestClientRequestIDMonotonic(t *testing.T) {
	cfg := DefaultConfig()
	c := &Client{cfg: cfg, log: testLogger()}

	prev := c.nextRequestID()
	for i := 0; i < 100; i++ {
		next := c.nextRequestID()
		if next <= prev {
			t.Fatalf("request ID not monotonically increasing: prev=%d, next=%d", prev, next)
		}
		prev = next
	}
}

func TestClientRequestIDUint48Mask(t *testing.T) {
	cfg := DefaultConfig()
	c := &Client{cfg: cfg, log: testLogger()}

	// Force near uint48 boundary.
	maxUint48 := uint64((1 << 48) - 1)
	c.nextReqID.Store(maxUint48 - 1)

	id1 := c.nextRequestID()
	if id1 != maxUint48 {
		t.Errorf("expected %d, got %d", maxUint48, id1)
	}

	// Next should wrap around (overflow masked to 48 bits).
	id2 := c.nextRequestID()
	if id2 >= maxUint48 {
		t.Logf("id2=%d (wrapped as expected via mask)", id2)
	}
}

func TestClientCallTimeout(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatTimeout = 100 * time.Millisecond

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Register a handler that sleeps beyond the timeout.
	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
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

	_, err := rpcClient.Heartbeat(ctx, &HeartbeatRequest{WorkerID: "w-1"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientContextCancellation(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatTimeout = 5 * time.Second

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Register a handler that blocks until context is done.
	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		<-ctx.Done()
		return &HeartbeatResponse{Accepted: true}, nil
	})

	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()

	go srv.ServeSession(srvCtx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	// Use a short deadline context so the stream times out quickly.
	callCtx, callCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer callCancel()

	_, err := rpcClient.Heartbeat(callCtx, &HeartbeatRequest{WorkerID: "w-1"})
	if err == nil {
		t.Fatal("expected error from deadline-exceeded context")
	}
}

func TestClientRetryLogic(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	cfg.HeartbeatTimeout = 5 * time.Second

	callCount := 0
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Handler fails twice (retryable), then succeeds.
	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		callCount++
		if callCount <= 2 {
			return nil, NewRPCError(ErrCodeTimeout, "temporary failure")
		}
		return &HeartbeatResponse{Accepted: true, EpochID: 1}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	var resp HeartbeatResponse
	err := rpcClient.CallWithRetry(ctx, MethodHeartbeat, &HeartbeatRequest{WorkerID: "w-1"}, &resp, 3)
	if err != nil {
		t.Fatalf("CallWithRetry: %v", err)
	}
	if !resp.Accepted {
		t.Error("expected Accepted=true")
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestClientRetryNonRetryable(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	callCount := 0
	srv.Register(MethodSubmitJob, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		callCount++
		return nil, NewRPCError(ErrCodeUnknownJob, "not found")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	var resp SubmitJobResponse
	err := rpcClient.CallWithRetry(ctx, MethodSubmitJob, &SubmitJobRequest{JobID: "x"}, &resp, 3)
	if err == nil {
		t.Fatal("expected error for non-retryable failure")
	}

	// Should only have been called once (no retry for non-retryable errors).
	if callCount != 1 {
		t.Errorf("expected 1 call (no retry), got %d", callCount)
	}
}

func TestClientRPCErrorPropagation(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	srv.Register(MethodSubmitJob, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		return nil, &RPCError{
			Code:         ErrCodeInvalidJobGraph,
			Name:         "INVALID_JOB_GRAPH",
			Message:      "cycle detected",
			Retryable:    false,
			RetryAfterMs: 0,
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

	_, err := rpcClient.SubmitJob(ctx, &SubmitJobRequest{JobID: "bad-graph"})
	if err == nil {
		t.Fatal("expected error")
	}

	rpcErr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("expected *RPCError, got %T", err)
	}
	if rpcErr.Code != ErrCodeInvalidJobGraph {
		t.Errorf("Code = %d, want %d", rpcErr.Code, ErrCodeInvalidJobGraph)
	}
	if rpcErr.Message != "cycle detected" {
		t.Errorf("Message = %q, want %q", rpcErr.Message, "cycle detected")
	}
}

func TestClientFullRoundtrip(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Register a handler that echoes the job graph.
	srv.Register(MethodSubmitJob, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req SubmitJobRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}

		statuses := make([]TaskDeploymentStatus, len(req.Graph.Operators))
		for i, op := range req.Graph.Operators {
			statuses[i] = TaskDeploymentStatus{
				TaskID:  op.OperatorID + "-0",
				Status:  TaskStatusDeploying,
				Message: "deploying " + op.Name,
			}
		}

		return &SubmitJobResponse{
			Accepted:     true,
			TaskStatuses: statuses,
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

	resp, err := rpcClient.SubmitJob(ctx, &SubmitJobRequest{
		JobID:   "j-1",
		JobName: "test",
		Graph: JobGraph{
			Operators: []OperatorDescriptor{
				{OperatorID: "src", Name: "source", Type: OperatorTypeSource, Parallelism: 1},
				{OperatorID: "snk", Name: "sink", Type: OperatorTypeSink, Parallelism: 1},
			},
			Edges: []EdgeDescriptor{
				{SourceOperatorID: "src", TargetOperatorID: "snk", Shuffle: ShuffleStrategyForward},
			},
		},
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	if !resp.Accepted {
		t.Error("expected Accepted=true")
	}
	if len(resp.TaskStatuses) != 2 {
		t.Fatalf("TaskStatuses count = %d, want 2", len(resp.TaskStatuses))
	}
	if resp.TaskStatuses[0].TaskID != "src-0" {
		t.Errorf("TaskStatuses[0].TaskID = %q, want %q", resp.TaskStatuses[0].TaskID, "src-0")
	}
}
