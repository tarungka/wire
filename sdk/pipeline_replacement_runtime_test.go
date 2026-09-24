package sdk

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tarungka/wire/internal/coordinator"
)

func TestSameJobReplacementRestoresSourceAndNewCode(t *testing.T) {
	for _, transactional := range []bool{false, true} {
		kind := "ordinary"
		if transactional {
			kind = "transactional"
		}
		t.Run(kind, func(t *testing.T) {
			for _, scenario := range []string{"success", "rollback", "no-restart"} {
				t.Run(scenario, func(t *testing.T) {
					testSameJobReplacement(t, scenario != "success", scenario != "no-restart", transactional, "")
				})
			}
		})
	}
}

func TestSameJobReplacementLostHTTPResponse(t *testing.T) {
	for _, scenario := range []string{"success", "rollback", "no-restart"} {
		t.Run(scenario, func(t *testing.T) {
			testSameJobReplacement(t, scenario != "success", scenario != "no-restart", true, "http")
		})
	}
}

func TestSameJobReplacementLostSavepointHTTPResponse(t *testing.T) {
	for _, scenario := range []string{"success", "rollback", "no-restart"} {
		t.Run(scenario, func(t *testing.T) {
			testSameJobReplacement(t, scenario != "success", scenario != "no-restart", true, "savepoint-http")
		})
	}
}

func TestSameJobReplacementTransientStatusReads(t *testing.T) {
	for _, scenario := range []string{"success", "rollback", "no-restart"} {
		t.Run(scenario, func(t *testing.T) {
			testSameJobReplacement(t, scenario != "success", scenario != "no-restart", true, "poll")
		})
	}
}

func TestReplacementInsertsStatelessOperator(t *testing.T) {
	for _, transactional := range []bool{false, true} {
		kind := "ordinary"
		if transactional {
			kind = "transactional"
		}
		t.Run(kind, func(t *testing.T) {
			for _, scenario := range []string{"success", "rollback", "no-restart"} {
				t.Run(scenario, func(t *testing.T) {
					testSameJobReplacement(t, scenario != "success", scenario != "no-restart", transactional, "insert")
				})
			}
		})
	}
}

func TestSameJobReplacementLostCommitResponse(t *testing.T) {
	for _, boundary := range []string{"savepoint", "replacement-final"} {
		t.Run(boundary, func(t *testing.T) {
			for _, scenario := range []string{"success", "rollback", "no-restart"} {
				if boundary == "replacement-final" && scenario == "no-restart" {
					continue // A failed job never reaches the replacement's final commit.
				}
				t.Run(scenario, func(t *testing.T) {
					testSameJobReplacement(t, scenario != "success", scenario != "no-restart", true, boundary)
				})
			}
		})
	}
}

func testSameJobReplacement(t *testing.T, fail, recovery, transactional bool, responseLoss string) {
	release := make(chan struct{})
	restored := make(chan uint64, 4)
	var closed atomic.Int32
	registry := NewWorkerRegistry()
	registry.RegisterSource("replay", func(context.Context, []byte, WorkerTaskContext) (Source, error) {
		return &pauseReplaySource{release: release, restored: restored, closed: &closed}, nil
	})
	for _, version := range []string{"v1", "v2"} {
		registry.RegisterMap(version, func(context.Context, []byte, WorkerTaskContext) (MapFunc, error) {
			if fail && version == "v2" {
				return nil, errors.New("replacement factory unavailable")
			}
			return func(e Event) (Event, error) { e.Value = append([]byte(version+":"), e.Value...); return e, nil }, nil
		})
	}
	registry.RegisterMap("extra", func(context.Context, []byte, WorkerTaskContext) (MapFunc, error) {
		return func(e Event) (Event, error) { e.Value = append([]byte("extra:"), e.Value...); return e, nil }, nil
	})
	output := &collectSink{}
	ledger := &pauseTransactionLedger{prepared: map[uint64][]string{}, committed: map[uint64]bool{}, loseResponse: responseLoss == "savepoint"}
	t.Cleanup(func() {
		ledger.mu.Lock()
		defer ledger.mu.Unlock()
		if ledger.loseResponse {
			t.Error("lost commit response was never injected")
		}
	})
	registry.RegisterSink("output", func(context.Context, []byte, WorkerTaskContext) (Sink, error) {
		if transactional {
			return &pauseTransactionSink{ledger: ledger, observed: output}, nil
		}
		return &pipelineRemoteSink{target: output}, nil
	})
	ctx, coord, endpoint := lifecycleCluster(t, registry)
	env := New().SetMode(Cluster).SetCoordinator(endpoint)
	if recovery {
		env.SetRestartStrategy(FixedDelay(3, 0))
	}
	env.AddSourceNamed("source", "replay", nil).MapNamed("map", "v1", nil).AddSinkNamed("sink", "output", nil)
	done := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "replacement-runtime"); done <- err }()
	var jobID string
	lifecycleWait(t, ctx, func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) != 1 || jobs[0].Status != coordinator.JobRunning || len(output.Events()) != 1 {
			return false
		}
		jobID = jobs[0].ID
		return true
	})
	old, err := coord.GetJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	candidateEnv := New().SetMode(Cluster).SetCoordinator(endpoint)
	if recovery {
		candidateEnv.SetRestartStrategy(FixedDelay(3, 0))
	}
	candidateStream := candidateEnv.AddSourceNamed("source", "replay", nil).MapNamed("map", "v2", nil)
	if responseLoss == "insert" {
		candidateStream = candidateStream.MapNamed("extra", "extra", nil)
	}
	candidateStream.AddSinkNamed("sink", "output", nil)
	if responseLoss == "http" || responseLoss == "savepoint-http" || responseLoss == "poll" {
		target, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		var dropped, savepointReads, jobReads, savepointPosts, replacementPosts atomic.Int32
		director := proxy.Director
		proxy.Director = func(request *http.Request) {
			director(request)
			if request.Method == http.MethodPost {
				switch request.URL.Path {
				case "/api/v1/jobs/" + jobID + "/savepoints":
					savepointPosts.Add(1)
				case "/api/v1/jobs/" + jobID + "/replacement":
					replacementPosts.Add(1)
				}
			}
		}
		dropPath := "/api/v1/jobs/" + jobID + "/replacement"
		if responseLoss == "savepoint-http" {
			dropPath = "/api/v1/jobs/" + jobID + "/savepoints"
		}
		proxy.ModifyResponse = func(response *http.Response) error {
			if responseLoss == "poll" {
				fail := false
				if response.Request.Method == http.MethodGet {
					if strings.HasPrefix(response.Request.URL.Path, "/api/v1/jobs/"+jobID+"/savepoints/") {
						fail = savepointReads.Add(1) <= 2
					}
					if response.Request.URL.Path == "/api/v1/jobs/"+jobID {
						fail = jobReads.Add(1) <= 2
					}
				}
				if fail {
					_ = response.Body.Close()
					response.Body = io.NopCloser(strings.NewReader(""))
					response.ContentLength = 0
					response.Header.Del("Content-Length")
					response.StatusCode = http.StatusServiceUnavailable
				}
				return nil
			}
			if response.Request.URL.Path == dropPath && response.StatusCode == http.StatusAccepted {
				dropped.Add(1)
				return errors.New("injected lost accepted replacement reply")
			}
			return nil
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}
		server := httptest.NewServer(proxy)
		t.Cleanup(func() {
			server.Close()
			if savepointPosts.Load() != 1 || replacementPosts.Load() != 1 {
				t.Errorf("mutation retries: savepoint=%d replacement=%d", savepointPosts.Load(), replacementPosts.Load())
			}
			if responseLoss == "poll" {
				if savepointReads.Load() < 3 || jobReads.Load() < 3 {
					t.Errorf("read failures not exercised: savepoint=%d job=%d", savepointReads.Load(), jobReads.Load())
				}
			} else if dropped.Load() != 1 {
				t.Errorf("dropped replies=%d", dropped.Load())
			}
		})
		candidateEnv.SetCoordinator(server.URL)
	}
	candidate := &YAMLPipeline{Name: old.Name, env: candidateEnv}
	result, reloadErr := candidate.Reload(ctx, jobID)
	if (!fail && reloadErr != nil) || (fail && !errors.Is(reloadErr, ErrPipelineReplacementRolledBack)) {
		t.Fatalf("reload=%+v err=%v", result, reloadErr)
	}
	if result.JobID != jobID || result.SavepointID == "" || result.RolledBack != fail {
		t.Fatalf("reload result=%+v", result)
	}

	if responseLoss == "savepoint-http" {
		savepoints, err := coord.ListSavepoints(jobID)
		if err != nil || len(savepoints) != 1 || savepoints[0].ID != result.SavepointID {
			t.Fatalf("savepoint creation duplicated or lost: %+v %v", savepoints, err)
		}
	}
	if !recovery {
		lifecycleWait(t, ctx, func() bool { job, err := coord.GetJob(jobID); return err == nil && job.Status == coordinator.JobFailed })
		job, _ := coord.GetJob(jobID)
		if string(job.Config) != string(old.Config) || job.RescaleFailure == "" {
			t.Fatal("no-restart failure lost original config")
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("failed replacement reported success")
			}
		case <-ctx.Done():
			t.Fatal("job result not delivered")
		}
		if transactional {
			ledger.mu.Lock()
			visible := append([]string(nil), ledger.visible...)
			ledger.mu.Unlock()
			if len(visible) != 1 || visible[0] != "v1:first" {
				t.Fatalf("disabled recovery commits=%v", visible)
			}
		}
		if len(output.Events()) != 1 {
			t.Fatal("disabled recovery emitted extra output")
		}
		return
	}
	select {
	case offset := <-restored:
		if offset != 1 {
			t.Fatalf("offset=%d", offset)
		}
	case <-ctx.Done():
		job, _ := coord.GetJob(jobID)
		t.Fatalf("replacement never restored source: %+v", job)
	}
	lifecycleWait(t, ctx, func() bool {
		job, err := coord.GetJob(jobID)
		return err == nil && job.Status == coordinator.JobRunning && job.DeploymentGeneration > old.DeploymentGeneration
	})
	if (!fail && closed.Load() != 1) || closed.Load() < 1 {
		t.Fatalf("old source not closed before replacement: %d", closed.Load())
	}
	if responseLoss == "replacement-final" {
		// No periodic checkpoints are configured. Arm only after replacement
		// or rollback has restored the first savepoint, before EOF can trigger
		// the final checkpoint with the second record.
		ledger.mu.Lock()
		ledger.loseResponse = true
		ledger.mu.Unlock()
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("replacement did not finish")
	}
	if fail {
		job, err := coord.GetJob(jobID)
		if err != nil || job.RescaleFailure == "" || string(job.Config) != string(old.Config) {
			t.Fatalf("rollback status=%+v err=%v", job, err)
		}
	}
	expected := "v2:second"
	if responseLoss == "insert" {
		expected = "extra:v2:second"
	}
	if fail {
		expected = "v1:second"
	}
	events := output.Events()
	if len(events) != 2 || string(events[0].Value) != "v1:first" || string(events[1].Value) != expected {
		t.Fatalf("output=%v", events)
	}
	if transactional {
		ledger.mu.Lock()
		defer ledger.mu.Unlock()
		if len(ledger.visible) != 2 || ledger.visible[0] != "v1:first" || ledger.visible[1] != expected {
			t.Fatalf("external commits=%v", ledger.visible)
		}
		if ledger.jobID != jobID || ledger.generation <= old.DeploymentGeneration {
			t.Fatalf("transaction authority not preserved: job=%s generation=%d", ledger.jobID, ledger.generation)
		}
	}
	if len(coord.ListJobs(nil)) != 1 {
		t.Fatal("replacement created another job")
	}
}
