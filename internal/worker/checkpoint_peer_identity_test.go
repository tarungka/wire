package worker

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCheckpointReplicaForwardsVerifiedUploader(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	left, right := net.Pipe()
	caller, err := yamux.Client(left, nil)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := yamux.Server(right, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan rpc.AuthorizeCheckpointReplicaRequest, 1)
	server := rpc.NewServer(rpc.DefaultConfig())
	server.Register(rpc.MethodAuthorizeCheckpointReplica, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var request rpc.AuthorizeCheckpointReplicaRequest
		if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &request); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, "bad request")
		}
		observed <- request
		return &rpc.AcknowledgeCheckpointResponse{Accepted: request.SourceWorkerID == "worker" && request.WorkerID == "replica"}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, receiver) }()
	defer func() { cancel(); caller.Close(); receiver.Close(); server.Stop(); <-done }()
	peerTLS := testPeerTLS(t)
	replica := &Worker{cfg: Config{WorkerID: "replica", PeerTLSConfig: peerTLS}, client: rpc.NewClient(caller, rpc.DefaultConfig())}
	address, stop, err := startCheckpointReplicaService(ctx, CheckpointReplicaConfig{TLSConfig: peerTLS, ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 1, Authorize: replica.authorizeCheckpointReplica})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	uploader := archiveCheckpointReplicator{jobID: "job", taskID: "task", epoch: 2, stagingRoot: t.TempDir(), client: &reconnectingCheckpointClient{address: address, tlsConfig: peerTLS}}
	if err := uploader.Replicate(ctx, engine.TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2}); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-observed:
		if request.SourceWorkerID != "worker" || request.Snapshot.TaskID != "task" {
			t.Fatalf("request=%+v", request)
		}
	case <-ctx.Done():
		t.Fatal("missing identity attestation")
	}
	if err := replica.authorizeCheckpointReplica(ctx, rpc.ReplicateCheckpointRequest{}); err == nil {
		t.Fatal("secure replica authorized upload without verified session context")
	}
	select {
	case <-observed:
		t.Fatal("unverified request reached coordinator")
	default:
	}
}
