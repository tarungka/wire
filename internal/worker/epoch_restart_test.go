package worker

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

func TestRestartedWorkerAdvertisesPersistedEpochAndRejectsOlderLeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "epoch")
	previous, err := openEpochStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := previous.save(9); err != nil {
		t.Fatal(err)
	}
	if err := previous.close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(rpc.DefaultConfig())
	requested := make(chan uint64, 1)
	var heartbeats atomic.Int32
	server.Register(rpc.MethodRegisterWorker, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var request rpc.RegisterWorkerRequest
		if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &request); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
		}
		requested <- request.HighestSeenEpoch
		return &rpc.RegisterWorkerResponse{Epoch: 8}, nil
	})
	server.Register(rpc.MethodHeartbeat, func(context.Context, uint64, []byte) (any, *rpc.RPCError) {
		heartbeats.Add(1)
		return &rpc.HeartbeatResponse{EpochID: 8, Accepted: true}, nil
	})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		session, err := transport.NewServerSession(conn, transport.DefaultConfig())
		if err != nil {
			_ = conn.Close()
			return
		}
		defer session.Close()
		server.ServeSession(ctx, session.YamuxSession())
	}()
	defer func() { cancel(); _ = listener.Close(); server.Stop(); <-finished }()
	restarted := New(Config{WorkerID: "restarted", EpochPath: path, CoordinatorAddr: listener.Addr().String(), TaskSlots: 1, HeartbeatInterval: 20 * time.Millisecond, HeartbeatTimeout: 300 * time.Millisecond}, zerolog.Nop())
	done := make(chan error, 1)
	go func() { done <- restarted.Run(ctx) }()
	select {
	case epoch := <-requested:
		if epoch != 9 {
			t.Fatalf("forgot persisted fence: %d", epoch)
		}
	case <-ctx.Done():
		t.Fatal("worker never registered")
	}
	if err := <-done; !errors.Is(err, ErrCoordinatorContactLost) {
		t.Fatalf("stale leader maintained worker authority: %v", err)
	}
	if heartbeats.Load() != 0 {
		t.Fatal("worker entered heartbeat loop after stale registration response")
	}
	after, err := openEpochStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = after.close() }()
	if after.epoch != 9 {
		t.Fatal("old leader rolled back durable epoch")
	}
}
