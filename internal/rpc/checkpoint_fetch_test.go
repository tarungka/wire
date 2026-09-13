package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type trackedArchive struct {
	io.Reader
	closed *atomic.Bool
}

func (a trackedArchive) Close() error { a.closed.Store(true); return nil }

func TestFetchCheckpointArchive(t *testing.T) {
	for _, mode := range []string{"valid", "checksum", "identity", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			clientSession, serverSession := testYamuxPair(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			body := bytes.Repeat([]byte("x"), CheckpointChunkSize+19)
			var closed atomic.Bool
			handler, err := NewCheckpointFetchHandler(1, func(_ context.Context, r FetchCheckpointRequest) (ReplicateCheckpointRequest, io.ReadCloser, error) {
				snapshot := ReplicateCheckpointRequest{Format: CheckpointFormatArchive, JobID: r.JobID, TaskID: r.TaskID, CheckpointID: r.CheckpointID, EpochID: r.EpochID, Size: uint64(len(body)), SHA256: sha256.Sum256(body)}
				data := body
				switch mode {
				case "checksum":
					snapshot.SHA256[0]++
				case "identity":
					snapshot.EpochID++
				case "truncated":
					data = body[:3]
				}
				return snapshot, trackedArchive{bytes.NewReader(data), &closed}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			server := NewServer(DefaultConfig())
			server.RegisterStream(MethodFetchCheckpoint, handler)
			serverDone := make(chan struct{})
			go func() { defer close(serverDone); server.ServeSession(ctx, serverSession) }()
			defer func() { cancel(); _ = serverSession.Close(); <-serverDone }()
			request := FetchCheckpointRequest{WorkerID: "worker", DeploymentEpoch: 4, JobID: "job", TaskID: "task", CheckpointID: 7, EpochID: 2}
			var result bytes.Buffer
			err = NewClient(clientSession, DefaultConfig()).FetchCheckpoint(ctx, request, &result)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("fetch result: %v", err)
			}
			if mode == "valid" && !bytes.Equal(result.Bytes(), body) {
				t.Fatal("archive changed")
			}
			// Joining the server makes resource cleanup part of the assertion.
			cancel()
			_ = serverSession.Close()
			<-serverDone
			if !closed.Load() {
				t.Fatal("archive not closed")
			}
		})
	}
}

func TestFetchCancellationReleasesLoaderAndKeepsSession(t *testing.T) {
	clientSession, serverSession := testYamuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{})
	released := make(chan struct{})
	handler, err := NewCheckpointFetchHandler(1, func(ctx context.Context, _ FetchCheckpointRequest) (ReplicateCheckpointRequest, io.ReadCloser, error) {
		close(started)
		<-ctx.Done()
		close(released)
		return ReplicateCheckpointRequest{}, nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(DefaultConfig())
	server.RegisterStream(MethodFetchCheckpoint, handler)
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, serverSession) }()
	defer func() { cancel(); _ = serverSession.Close(); <-done }()
	fetchCtx, stopFetch := context.WithCancel(ctx)
	defer stopFetch()
	fetchDone := make(chan error, 1)
	go func() {
		fetchDone <- NewClient(clientSession, DefaultConfig()).FetchCheckpoint(fetchCtx, FetchCheckpointRequest{WorkerID: "worker", DeploymentEpoch: 4, JobID: "job", TaskID: "task", CheckpointID: 7, EpochID: 2}, io.Discard)
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("loader did not start")
	}
	stopFetch()
	select {
	case err := <-fetchDone:
		if err == nil {
			t.Fatal("cancelled fetch succeeded")
		}
	case <-ctx.Done():
		t.Fatal("fetch remained blocked")
	}
	select {
	case <-released:
	case <-ctx.Done():
		t.Fatal("remote loader remained blocked")
	}
	if clientSession.IsClosed() || serverSession.IsClosed() {
		t.Fatal("fetch cancellation closed shared session")
	}
	sibling, err := clientSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	_ = sibling.Close()
}
