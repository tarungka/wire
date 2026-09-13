package worker

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

// CheckpointReplicaConfig enables a worker replica endpoint. Directories must
// exist and be owned by this worker. Authorize must validate current assignment
// ownership before a verified transfer can be published.
type CheckpointReplicaConfig struct {
	ListenAddr    string
	AdvertiseAddr string
	StoreRoot     string
	ArtifactRoot  string
	StagingRoot   string
	Concurrency   int
	Authorize     func(context.Context, rpc.ReplicateCheckpointRequest) error
}

func startCheckpointReplicaService(ctx context.Context, cfg CheckpointReplicaConfig) (string, func(), error) {
	if cfg.Authorize == nil {
		return "", nil, fmt.Errorf("checkpoint replica authorization is required")
	}
	info, err := os.Stat(cfg.ArtifactRoot)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("checkpoint artifact root is not a directory")
	}
	store, err := engine.NewFileCheckpointStore(cfg.StoreRoot)
	if err != nil {
		return "", nil, err
	}
	handler, err := rpc.NewCheckpointReplicaHandler(cfg.StagingRoot, cfg.Concurrency, func(ctx context.Context, request rpc.ReplicateCheckpointRequest, body io.Reader) error {
		if err := cfg.Authorize(ctx, request); err != nil {
			return err
		}
		return publishCheckpointReplica(ctx, store, cfg.ArtifactRoot, request, body)
	})
	if err != nil {
		return "", nil, err
	}
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return "", nil, err
	}
	address := cfg.AdvertiseAddr
	if address == "" {
		address = listener.Addr().String()
	}
	host, port, err := net.SplitHostPort(address)
	portNumber, portErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || host == "" || (ip != nil && ip.IsUnspecified()) || portErr != nil || portNumber < 1 || portNumber > 65535 {
		_ = listener.Close()
		return "", nil, fmt.Errorf("checkpoint replica requires a reachable advertised host and port")
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	server := rpc.NewServer(rpc.DefaultConfig())
	server.RegisterStream(rpc.MethodReplicateCheckpoint, handler)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var connections sync.WaitGroup
		defer connections.Wait()
		stop := context.AfterFunc(serviceCtx, func() { _ = listener.Close() })
		defer stop()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				defer conn.Close()
				stop := context.AfterFunc(serviceCtx, func() { _ = conn.Close() })
				defer stop()
				session, err := transport.NewServerSession(conn, transport.DefaultConfig())
				if err != nil {
					return
				}
				defer session.Close()
				server.ServeSession(serviceCtx, session.YamuxSession())
			}()
		}
	}()
	var once sync.Once
	closeService := func() { once.Do(func() { cancel(); _ = listener.Close(); <-done; server.Stop() }) }
	return address, closeService, nil
}

func (w *Worker) authorizeCheckpointReplica(ctx context.Context, snapshot rpc.ReplicateCheckpointRequest) error {
	w.mu.RLock()
	client := w.client
	w.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("checkpoint coordinator connection is unavailable")
	}
	var response rpc.AcknowledgeCheckpointResponse
	if err := client.Call(ctx, rpc.MethodAuthorizeCheckpointReplica, rpc.AuthorizeCheckpointReplicaRequest{WorkerID: w.cfg.WorkerID, Snapshot: snapshot}, &response); err != nil {
		return err
	}
	if !response.Accepted {
		return fmt.Errorf("checkpoint publication rejected: %s", response.Message)
	}
	return nil
}
