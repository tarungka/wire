package rpc

import (
	"context"
	"sync"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
)

func TestServerHandlerDispatch(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Register a heartbeat handler.
	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req HeartbeatRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &HeartbeatResponse{
			Accepted: true,
			EpochID:  req.EpochID,
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	// Call via client.
	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	resp, err := rpcClient.Heartbeat(ctx, &HeartbeatRequest{
		WorkerID: "w-1",
		EpochID:  42,
	})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !resp.Accepted {
		t.Error("expected Accepted=true")
	}
	if resp.EpochID != 42 {
		t.Errorf("EpochID = %d, want 42", resp.EpochID)
	}
}

func TestServerUnknownMethod(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Don't register any handlers.
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
		t.Fatal("expected error for unknown method")
	}

	rpcErr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("expected *RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != ErrCodeUnknownMethod {
		t.Errorf("Code = %d, want %d", rpcErr.Code, ErrCodeUnknownMethod)
	}
}

func TestServerErrorResponse(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Register handler that returns an error.
	srv.Register(MethodSubmitJob, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		return nil, NewRPCError(ErrCodeUnknownJob, "job not found")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	_, err := rpcClient.SubmitJob(ctx, &SubmitJobRequest{JobID: "unknown"})
	if err == nil {
		t.Fatal("expected error")
	}

	rpcErr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("expected *RPCError, got %T", err)
	}
	if rpcErr.Code != ErrCodeUnknownJob {
		t.Errorf("Code = %d, want %d", rpcErr.Code, ErrCodeUnknownJob)
	}
}

func TestServerConcurrentRPCs(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	srv.Register(MethodRequestTaskSlots, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req RequestTaskSlotsRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &RequestTaskSlotsResponse{
			Granted:        req.RequiredSlots,
			AvailableSlots: 10,
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

	const numConcurrent = 10
	var wg sync.WaitGroup
	errs := make([]error, numConcurrent)

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := rpcClient.RequestTaskSlots(ctx, &RequestTaskSlotsRequest{
				JobID:         "job-1",
				RequiredSlots: int32(idx + 1),
			})
			if err != nil {
				errs[idx] = err
				return
			}
			if resp.Granted != int32(idx+1) {
				t.Errorf("request %d: Granted = %d, want %d", idx, resp.Granted, idx+1)
			}
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d failed: %v", i, err)
		}
	}
}

func TestServerAllSixRPCs(t *testing.T) {
	client, server := testYamuxPair(t)

	cfg := DefaultConfig()
	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}

	// Register all 6 handlers.
	srv.Register(MethodSubmitJob, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req SubmitJobRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &SubmitJobResponse{Accepted: true, Message: "ok"}, nil
	})

	srv.Register(MethodUpdateTaskStatus, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req UpdateTaskStatusRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &UpdateTaskStatusResponse{Accepted: true}, nil
	})

	srv.Register(MethodTriggerCheckpoint, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req TriggerCheckpointRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &TriggerCheckpointResponse{Accepted: true}, nil
	})

	srv.Register(MethodAcknowledgeCheckpoint, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req AcknowledgeCheckpointRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &AcknowledgeCheckpointResponse{Accepted: true}, nil
	})

	srv.Register(MethodRequestTaskSlots, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req RequestTaskSlotsRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &RequestTaskSlotsResponse{Granted: req.RequiredSlots, AvailableSlots: 8}, nil
	})

	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req HeartbeatRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &HeartbeatResponse{Accepted: true, EpochID: req.EpochID}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ServeSession(ctx, server)

	rpcClient := &Client{
		session: client,
		cfg:     cfg,
		log:     testLogger(),
	}

	// Test each RPC.
	t.Run("SubmitJob", func(t *testing.T) {
		resp, err := rpcClient.SubmitJob(ctx, &SubmitJobRequest{JobID: "j-1"})
		if err != nil {
			t.Fatalf("SubmitJob: %v", err)
		}
		if !resp.Accepted {
			t.Error("expected Accepted=true")
		}
	})

	t.Run("UpdateTaskStatus", func(t *testing.T) {
		resp, err := rpcClient.UpdateTaskStatus(ctx, &UpdateTaskStatusRequest{
			JobID: "j-1", TaskID: "t-1", Status: TaskStatusRunning,
		})
		if err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}
		if !resp.Accepted {
			t.Error("expected Accepted=true")
		}
	})

	t.Run("TriggerCheckpoint", func(t *testing.T) {
		resp, err := rpcClient.TriggerCheckpoint(ctx, &TriggerCheckpointRequest{
			JobID: "j-1", CheckpointID: 1, EpochID: 1, Type: CheckpointTypePeriodic,
		})
		if err != nil {
			t.Fatalf("TriggerCheckpoint: %v", err)
		}
		if !resp.Accepted {
			t.Error("expected Accepted=true")
		}
	})

	t.Run("AcknowledgeCheckpoint", func(t *testing.T) {
		resp, err := rpcClient.AcknowledgeCheckpoint(ctx, &AcknowledgeCheckpointRequest{
			JobID: "j-1", TaskID: "t-1", CheckpointID: 1, EpochID: 1,
		})
		if err != nil {
			t.Fatalf("AcknowledgeCheckpoint: %v", err)
		}
		if !resp.Accepted {
			t.Error("expected Accepted=true")
		}
	})

	t.Run("RequestTaskSlots", func(t *testing.T) {
		resp, err := rpcClient.RequestTaskSlots(ctx, &RequestTaskSlotsRequest{
			JobID: "j-1", RequiredSlots: 4,
		})
		if err != nil {
			t.Fatalf("RequestTaskSlots: %v", err)
		}
		if resp.Granted != 4 {
			t.Errorf("Granted = %d, want 4", resp.Granted)
		}
	})

	t.Run("Heartbeat", func(t *testing.T) {
		resp, err := rpcClient.Heartbeat(ctx, &HeartbeatRequest{
			WorkerID: "w-1", EpochID: 99,
		})
		if err != nil {
			t.Fatalf("Heartbeat: %v", err)
		}
		if resp.EpochID != 99 {
			t.Errorf("EpochID = %d, want 99", resp.EpochID)
		}
	})
}
