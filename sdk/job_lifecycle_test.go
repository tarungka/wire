package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/jobcli"
)

// lifecycleCluster runs the production HTTP, RPC, scheduler and public worker
// API. Each test owns and joins its services and can inspect coordinator state.
func lifecycleCluster(t *testing.T, registry *WorkerRegistry) (context.Context, *coordinator.Coordinator, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	store := coordinator.NewMemoryStore()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "lifecycle", HeartbeatInterval: 100 * time.Millisecond}, store, nil, zerolog.Nop())
	var joined sync.WaitGroup
	start := func(fn func()) { joined.Add(1); go func() { defer joined.Done(); fn() }() }
	rpcServer := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	httpServer := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
	t.Cleanup(func() {
		cancel()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = httpServer.Shutdown(shutdown)
		_ = rpcServer.Shutdown(shutdown)
		joined.Wait()
		_ = store.Close()
	})
	start(func() { _ = coord.Run(ctx) })
	lifecycleWait(t, ctx, coord.IsReady)
	if err := rpcServer.Listen(); err != nil {
		t.Fatal(err)
	}
	start(func() { _ = rpcServer.Serve(ctx) })
	if err := httpServer.Listen(); err != nil {
		t.Fatal(err)
	}
	start(func() { _ = httpServer.Serve() })
	for i := 0; i < 2; i++ {
		cfg := WorkerConfig{WorkerID: fmt.Sprint("lifecycle-", i), CoordinatorAddr: rpcServer.Addr(), TaskSlots: 4, HeartbeatInterval: 100 * time.Millisecond, CheckpointDirectory: t.TempDir()}
		start(func() {
			if err := RunWorker(ctx, cfg, registry); err != nil && ctx.Err() == nil {
				t.Error(err)
			}
		})
	}
	lifecycleWait(t, ctx, func() bool { return len(coord.ListWorkers()) == 2 })
	return ctx, coord, "http://" + httpServer.Addr()
}

func lifecycleWait(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal("lifecycle condition timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

type cancellationSource struct{ closing, closed, release chan struct{} }

func (*cancellationSource) Open(context.Context) error { return nil }
func (*cancellationSource) GenerateWatermark() int64   { return 0 }
func (*cancellationSource) ReadBatch(ctx context.Context) ([]Event, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s *cancellationSource) Close() error {
	close(s.closing)
	<-s.release
	close(s.closed)
	return nil
}

func TestCLICancelWaitsForWorkerTeardown(t *testing.T) {
	source := &cancellationSource{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(source.release) })
	registry := NewWorkerRegistry()
	registry.RegisterSource("blocked", func(context.Context, []byte, WorkerTaskContext) (Source, error) { return source, nil })
	registry.RegisterSink("discard", func(context.Context, []byte, WorkerTaskContext) (Sink, error) { return &collectSink{}, nil })
	ctx, coord, url := lifecycleCluster(t, registry)
	env := New().SetMode(Cluster).SetCoordinator(url)
	env.AddSourceNamed("source", "blocked", nil).AddSinkNamed("sink", "discard", nil)
	result := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "cancel-lifecycle"); result <- err }()
	var jobID string
	lifecycleWait(t, ctx, func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) != 1 || jobs[0].Status != coordinator.JobRunning {
			return false
		}
		jobID = jobs[0].ID
		return true
	})
	var response bytes.Buffer
	if err := jobcli.Run(ctx, []string{"jobs", "cancel", jobID, "--coordinator", url}, &response, &response); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.closing:
	case <-ctx.Done():
		t.Fatal("worker did not cancel the blocked read")
	}
	job, err := coord.GetJob(jobID)
	if err != nil || job.Status != coordinator.JobCanceling {
		t.Fatalf("job finished before Close returned: %+v %v", job, err)
	}
	release.Do(func() { close(source.release) })
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled job reported successful execution")
		}
	case <-ctx.Done():
		t.Fatal("SDK did not observe terminal cancellation")
	}
	select {
	case <-source.closed:
	default:
		t.Fatal("terminal cancellation preceded connector teardown")
	}
	response.Reset()
	if err := jobcli.Run(ctx, []string{"jobs", "get", jobID, "--coordinator", url}, &response, &response); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Bytes(), &status); err != nil || status.Status != "CANCELED" {
		t.Fatalf("CLI status: %s, %v", response.String(), err)
	}
}
