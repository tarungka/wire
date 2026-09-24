package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/sdk"
	"github.com/tarungka/wire/sdk/connectors/httpapi"
	httpworker "github.com/tarungka/wire/sdk/connectors/httpapi/worker"
)

func TestPublicHTTPSourcePauseResumeRestoresSequence(t *testing.T) {
	testPublicHTTPSourceRestore(t, false)
}

func TestPublicHTTPSourceWorkerLossRestoresSequence(t *testing.T) {
	testPublicHTTPSourceRestore(t, true)
}

func testPublicHTTPSourceRestore(t *testing.T, workerLoss bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	store := coordinator.NewMemoryStore()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "source-test", WorkerTimeout: 3 * time.Second}, store, nil, zerolog.Nop())
	rpcServer := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	api := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
	var joined sync.WaitGroup
	start := func(fn func()) { joined.Add(1); go func() { defer joined.Done(); fn() }() }
	defer func() {
		cancel()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = api.Shutdown(shutdown)
		_ = rpcServer.Shutdown(shutdown)
		joined.Wait()
		_ = store.Close()
	}()
	wait := func(ready func() bool) {
		t.Helper()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatal("HTTP source cluster timed out")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	start(func() { _ = coord.Run(ctx) })
	wait(coord.IsReady)
	if err := rpcServer.Listen(); err != nil {
		t.Fatal(err)
	}
	if err := api.Listen(); err != nil {
		t.Fatal(err)
	}
	start(func() { _ = rpcServer.Serve(ctx) })
	start(func() { _ = api.Serve() })
	sources := make(chan *httpapi.Source, 4)
	var owners sync.Map
	events := make(chan sdk.Event, 4)
	for i := range 2 {
		workerCtx, stopWorker := context.WithCancel(ctx)
		defer stopWorker()
		registry := sdk.NewWorkerRegistry()
		factory := httpworker.SourceFactory()
		registry.RegisterSource("http-api", func(ctx context.Context, data []byte, tc sdk.WorkerTaskContext) (sdk.Source, error) {
			source, err := factory(ctx, data, tc)
			if err == nil {
				owners.Store(source.(*httpapi.Source), stopWorker)
				sources <- source.(*httpapi.Source)
			}
			return source, err
		})
		registry.RegisterSink("collect", func(context.Context, []byte, sdk.WorkerTaskContext) (sdk.Sink, error) {
			return &channelSink{events: events}, nil
		})
		cfg := sdk.WorkerConfig{WorkerID: fmt.Sprint("worker-", i), CoordinatorAddr: rpcServer.Addr(), TaskSlots: 2, HeartbeatInterval: 100 * time.Millisecond, HeartbeatTimeout: 3 * time.Second, CheckpointDirectory: t.TempDir()}
		start(func() {
			if err := sdk.RunWorker(workerCtx, cfg, registry); err != nil && workerCtx.Err() == nil {
				t.Error(err)
			}
		})
	}
	wait(func() bool { return len(coord.ListWorkers()) == 2 })
	config, err := httpworker.EncodeSourceConfig(httpapi.SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	env := sdk.New().SetMode(sdk.Cluster).SetCoordinator("http://" + api.Addr()).SetRestartStrategy(sdk.FixedDelay(3, 0))
	env.AddSourceNamed("ingress", "http-api", config).AddSinkNamed("sink", "collect", nil)
	execution := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "http-restore"); execution <- err }()
	nextSource := func() *httpapi.Source {
		t.Helper()
		select {
		case source := <-sources:
			wait(func() bool { return source.Address() != "" })
			return source
		case <-ctx.Done():
			t.Fatal("source was not opened")
			return nil
		}
	}
	send := func(source *httpapi.Source, value string, want uint64) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+source.Address()+"/ingest", strings.NewReader(fmt.Sprintf(`{"events":[{"key":"key","value":%q}]}`, value)))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var accepted struct {
			Sequence uint64 `json:"sequence"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&accepted)
		_ = response.Body.Close()
		if decodeErr != nil || response.StatusCode != 200 || accepted.Sequence != want {
			t.Fatalf("ingress: status=%d sequence=%d want=%d err=%v", response.StatusCode, accepted.Sequence, want, decodeErr)
		}
		select {
		case event := <-events:
			if string(event.Value) != value {
				t.Fatalf("event=%q want=%q", event.Value, value)
			}
		case <-ctx.Done():
			t.Fatal("event not delivered")
		}
	}
	first := nextSource()
	send(first, "before", 1)
	var jobID string
	wait(func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) == 1 && jobs[0].Status == coordinator.JobRunning {
			jobID = jobs[0].ID
			return true
		}
		return false
	})
	if workerLoss {
		checkpoint, err := coord.TriggerCheckpoint(jobID)
		if err != nil {
			t.Fatal(err)
		}
		wait(func() bool {
			job, err := coord.GetJob(jobID)
			return err == nil && job.LatestCheckpoint == checkpoint.ID
		})
		stop, ok := owners.Load(first)
		if !ok {
			t.Fatal("source worker ownership missing")
		}
		stop.(context.CancelFunc)()
	} else {
		if _, _, err := coord.PauseJob(jobID); err != nil {
			t.Fatal(err)
		}
		wait(func() bool { job, err := coord.GetJob(jobID); return err == nil && job.Status == coordinator.JobPaused })
		if _, err := coord.ResumeJob(jobID); err != nil {
			t.Fatal(err)
		}
	}
	restored := nextSource()
	if restored == first {
		t.Fatal("recovery reused source instance")
	}
	send(restored, "after", 2)
	if workerLoss {
		job, err := coord.GetJob(jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.RestartCount != 1 {
			t.Fatalf("worker loss caused %d restarts, want 1", job.RestartCount)
		}
	}
	if _, err := coord.CancelJob(jobID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-execution:
		if err == nil {
			t.Fatal("canceled job reported success")
		}
	case <-ctx.Done():
		t.Fatal("job did not cancel")
	}
}
