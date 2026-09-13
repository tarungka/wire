package worker

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

func TestWorkerReregistersAfterEpochChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := rpc.NewServer(rpc.DefaultConfig())
	var registrations atomic.Int32
	var addressMu sync.Mutex
	var replicaAddress string
	var changed atomic.Bool
	registeredAgain := make(chan struct{})
	server.Register(rpc.MethodRegisterWorker, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var request rpc.RegisterWorkerRequest
		if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &request); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
		}
		count := registrations.Add(1)
		addressMu.Lock()
		if count == 1 {
			replicaAddress = request.CheckpointAddress
		} else if replicaAddress != request.CheckpointAddress {
			changed.Store(true)
		}
		addressMu.Unlock()
		if count == 2 {
			close(registeredAgain)
		}
		return &rpc.RegisterWorkerResponse{Epoch: uint64(count)}, nil
	})
	server.Register(rpc.MethodHeartbeat, func(context.Context, uint64, []byte) (any, *rpc.RPCError) {
		return &rpc.HeartbeatResponse{Accepted: false, EpochID: 2}, nil
	})
	server.RegisterStream(rpc.MethodWatchCommands, func(ctx context.Context, _ uint64, _ []byte, _ *yamux.Stream) error { <-ctx.Done(); return nil })
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		var sessions sync.WaitGroup
		defer sessions.Wait()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			sessions.Add(1)
			go func() {
				defer sessions.Done()
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
				defer stop()
				session, err := transport.NewServerSession(conn, transport.DefaultConfig())
				if err != nil {
					return
				}
				defer session.Close()
				server.ServeSession(ctx, session.YamuxSession())
			}()
		}
	}()
	defer func() { cancel(); _ = listener.Close(); <-serverDone }()
	w := NewWithRegistry(Config{WorkerID: "worker", CoordinatorAddr: listener.Addr().String(), TaskSlots: 1, CheckpointReplica: &CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 1}}, NewRegistry(), zerolog.Nop())
	workerDone := make(chan error, 1)
	go func() { workerDone <- w.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-workerDone; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-registeredAgain:
	case <-ctx.Done():
		t.Fatal("worker did not re-register")
	}
	if changed.Load() {
		t.Fatal("reconnect replaced the replica endpoint")
	}
}
