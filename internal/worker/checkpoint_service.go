package worker

import (
	"context"
	"crypto/tls"
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
	TLSConfig      *tls.Config
	ListenAddr     string
	AdvertiseAddr  string
	StoreRoot      string
	ArtifactRoot   string
	StagingRoot    string
	Concurrency    int
	Authorize      func(context.Context, rpc.ReplicateCheckpointRequest) error
	AuthorizeFetch func(context.Context, rpc.FetchCheckpointRequest) error
}

func startCheckpointReplicaService(ctx context.Context, cfg CheckpointReplicaConfig) (string, func(), error) {
	if err := validatePeerTLS(cfg.TLSConfig); err != nil {
		return "", nil, err
	}
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
	if cfg.AuthorizeFetch != nil {
		fetchHandler, err := rpc.NewCheckpointFetchHandler(cfg.Concurrency, checkpointArchiveLoader(store, cfg.StagingRoot, func(ctx context.Context, request rpc.FetchCheckpointRequest) error {
			if cfg.TLSConfig != nil {
				name, ok := ctx.Value(checkpointPeerIdentityKey{}).(string)
				if !ok || name == "" || name != request.WorkerID {
					return fmt.Errorf("checkpoint fetch worker does not match verified certificate")
				}
			}
			return cfg.AuthorizeFetch(ctx, request)
		}))
		if err != nil {
			cancel()
			_ = listener.Close()
			return "", nil, err
		}
		server.RegisterStream(rpc.MethodFetchCheckpoint, fetchHandler)
	}
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
				transportConfig := transport.DefaultConfig()
				transportConfig.TLSConfig = cfg.TLSConfig
				session, err := transport.NewServerSession(conn, transportConfig)
				if err != nil {
					return
				}
				defer session.Close()
				sessionCtx := serviceCtx
				if cfg.TLSConfig != nil {
					state, ok := session.TLSConnectionState()
					if !ok || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 || state.PeerCertificates[0].Subject.CommonName == "" {
						return
					}
					sessionCtx = context.WithValue(serviceCtx, checkpointPeerIdentityKey{}, state.PeerCertificates[0].Subject.CommonName)
				}
				server.ServeSession(sessionCtx, session.YamuxSession())
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
	source, _ := ctx.Value(checkpointPeerIdentityKey{}).(string)
	if w.cfg.PeerTLSConfig != nil && source == "" {
		return fmt.Errorf("checkpoint upload has no verified worker identity")
	}
	var response rpc.AcknowledgeCheckpointResponse
	if err := client.Call(ctx, rpc.MethodAuthorizeCheckpointReplica, rpc.AuthorizeCheckpointReplicaRequest{WorkerID: w.cfg.WorkerID, SourceWorkerID: source, Snapshot: snapshot}, &response); err != nil {
		return err
	}
	if !response.Accepted {
		return fmt.Errorf("checkpoint publication rejected: %s", response.Message)
	}
	return nil
}

func (w *Worker) authorizeCheckpointFetch(ctx context.Context, fetch rpc.FetchCheckpointRequest) error {
	w.mu.RLock()
	client := w.client
	w.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("checkpoint coordinator connection is unavailable")
	}
	var response rpc.AcknowledgeCheckpointResponse
	if err := client.Call(ctx, rpc.MethodAuthorizeCheckpointFetch, rpc.AuthorizeCheckpointFetchRequest{ReplicaWorkerID: w.cfg.WorkerID, Fetch: fetch}, &response); err != nil {
		return err
	}
	if !response.Accepted {
		return fmt.Errorf("checkpoint recovery rejected: %s", response.Message)
	}
	return nil
}

type checkpointPeerIdentityKey struct{}
