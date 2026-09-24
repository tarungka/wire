package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/coordinator"
)

type yamlReloadSource struct{ *pauseReplaySource }

func (s *yamlReloadSource) ReadBatch(ctx context.Context) ([]Event, error) {
	events, err := s.pauseReplaySource.ReadBatch(ctx)
	for i := range events {
		events[i].Value, _ = json.Marshal(string(events[i].Value))
	}
	return events, err
}

func TestYAMLFileReplacementThroughWorkers(t *testing.T) {
	t.Run("same-layout", func(t *testing.T) { testYAMLFileReplacement(t, false) })
	t.Run("insert-transform", func(t *testing.T) { testYAMLFileReplacement(t, true) })
}

func testYAMLFileReplacement(t *testing.T, insert bool) {
	release := make(chan struct{})
	restored := make(chan uint64, 4)
	var closed atomic.Int32
	registry := NewWorkerRegistry()
	registry.RegisterPipelineTransforms()
	registry.RegisterSource("replay", func(context.Context, []byte, WorkerTaskContext) (Source, error) {
		return &yamlReloadSource{&pauseReplaySource{release: release, restored: restored, closed: &closed}}, nil
	})
	output := &collectSink{}
	ledger := &pauseTransactionLedger{prepared: map[uint64][]string{}, committed: map[uint64]bool{}}
	registry.RegisterSink("output", func(context.Context, []byte, WorkerTaskContext) (Sink, error) {
		return &pauseTransactionSink{ledger: ledger, observed: output}, nil
	})
	ctx, coord, url := lifecycleCluster(t, registry)
	original := `apiVersion: wire/v1
kind: Pipeline
metadata: {name: watched-runtime}
spec:
  sources:
    - {name: source, type: replay}
  transforms:
    - name: mapped
      type: map
      input: source
      config: {expression: '"v1:" + value'}
  sinks:
    - {name: sink, type: output, input: mapped}
`
	bindings := PipelineConnectors{NamedSources: map[string]string{"replay": "replay"}, NamedSinks: map[string]string{"output": "output"}}
	pipeline, err := ParsePipelineYAML([]byte(original), bindings)
	if err != nil {
		t.Fatal(err)
	}
	pipeline.SetCoordinator(url)
	execution := make(chan error, 1)
	go func() { _, err := pipeline.Execute(ctx); execution <- err }()
	var jobID string
	lifecycleWait(t, ctx, func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) != 1 || jobs[0].Status != coordinator.JobRunning || len(output.Events()) != 1 {
			return false
		}
		jobID = jobs[0].ID
		return true
	})
	path := filepath.Join(t.TempDir(), "pipeline.yaml")
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(path+".next", []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".next", path); err != nil {
			t.Fatal(err)
		}
	}
	write(original)
	watchCtx, stop := context.WithCancel(ctx)
	defer stop()
	applied := make(chan PipelineUpdatePlan, 4)
	rejected := make(chan error, 1)
	reloaded := make(chan PipelineReloadResult, 1)
	watched := make(chan error, 1)
	go func() {
		watched <- pipeline.WatchLiveUpdates(watchCtx, path, jobID, bindings, PipelineLiveWatchConfig{PipelineWatchConfig: PipelineWatchConfig{PollInterval: 5 * time.Millisecond, OnRejected: func(err error) { rejected <- err }}, AllowReplacement: true, OnApplied: func(plan PipelineUpdatePlan) { applied <- plan }, OnReload: func(result PipelineReloadResult, err error) {
			if err == nil {
				reloaded <- result
			}
		}})
	}()
	waitApply := func(kind PipelineUpdateKind) {
		t.Helper()
		select {
		case plan := <-applied:
			if plan.Kind != kind {
				t.Fatalf("plan=%+v", plan)
			}
		case err := <-watched:
			t.Fatalf("watch failed: %v", err)
		case <-ctx.Done():
			t.Fatal("watch timed out")
		}
	}
	waitApply(PipelineUnchanged)
	before, err := coord.GetJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	write("invalid: pipeline")
	select {
	case <-rejected:
	case <-ctx.Done():
		t.Fatal("invalid file not rejected")
	}
	after, err := coord.GetJob(jobID)
	if err != nil || after.Status != coordinator.JobRunning || after.DeploymentGeneration != before.DeploymentGeneration || closed.Load() != 0 {
		t.Fatal("invalid edit disturbed running deployment")
	}
	candidate := strings.Replace(original, "v1:", "v2:", 1)
	expected := `"v2:second"`
	if insert {
		candidate = strings.Replace(candidate, "  sinks:", `    - name: extra
      type: map
      input: mapped
      config: {expression: '"extra:" + value'}
  sinks:`, 1)
		candidate = strings.Replace(candidate, "type: output, input: mapped", "type: output, input: extra", 1)
		expected = `"extra:v2:second"`
	}
	write(candidate)
	waitApply(PipelineMigrationRequired)
	result := <-reloaded
	if result.SavepointID == "" || result.JobID != jobID || result.RolledBack {
		t.Fatalf("reload=%+v", result)
	}
	select {
	case offset := <-restored:
		if offset != 1 {
			t.Fatalf("offset=%d", offset)
		}
	case <-ctx.Done():
		t.Fatal("no restored source")
	}
	if closed.Load() != 1 {
		t.Fatalf("old source teardown=%d", closed.Load())
	}
	stop()
	if err := <-watched; !errors.Is(err, context.Canceled) {
		t.Fatalf("watch shutdown=%v", err)
	}
	close(release)
	select {
	case err := <-execution:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("job failed to finish")
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if len(ledger.visible) != 2 || ledger.visible[0] != `"v1:first"` || ledger.visible[1] != expected {
		t.Fatalf("committed output=%v", ledger.visible)
	}
}
