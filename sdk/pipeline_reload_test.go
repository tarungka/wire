package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPipelineFileLiveIntervalUpdates(t *testing.T) {
	var calls atomic.Int32
	updates := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/jobs/job/checkpoint-interval" {
			t.Errorf("unexpected mutation %s %s", r.Method, r.URL)
		}
		var body struct {
			Interval string `json:"interval"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		updates <- body.Interval
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "job", "checkpoint_interval": body.Interval})
	}))
	defer server.Close()
	original := strings.Replace(yamlPipelineHeader, "  sources:", "  checkpoint: {interval: 1s}\n  sources:", 1) + "  transforms:\n    - {name: mapped, type: map, input: input, config: {expression: 'value + 1'}}\n  sinks:\n    - {name: output, type: test-sink, input: mapped}\n"
	bindings := PipelineConnectors{NamedSources: map[string]string{"test-source": "source"}, NamedSinks: map[string]string{"test-sink": "sink"}}
	current, err := ParsePipelineYAML([]byte(original), bindings)
	if err != nil {
		t.Fatal(err)
	}
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
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	applied := make(chan PipelineUpdatePlan, 4)
	rejected := make(chan error, 4)
	done := make(chan error, 1)
	go func() {
		done <- current.SetCoordinator(server.URL).WatchLiveUpdates(ctx, path, "job", bindings, PipelineLiveWatchConfig{PipelineWatchConfig: PipelineWatchConfig{PollInterval: 5 * time.Millisecond, OnRejected: func(err error) { rejected <- err }}, OnApplied: func(plan PipelineUpdatePlan) { applied <- plan }})
	}()
	next := func(kind PipelineUpdateKind) {
		t.Helper()
		select {
		case plan := <-applied:
			if plan.Kind != kind {
				t.Fatalf("plan=%+v", plan)
			}
		case <-ctx.Done():
			t.Fatal("watch timeout")
		}
	}
	next(PipelineUnchanged)
	write("invalid: document")
	select {
	case <-rejected:
	case <-ctx.Done():
		t.Fatal("invalid edit not rejected")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid edit changed job")
	}
	write(strings.Replace(original, "interval: 1s", "interval: 2s", 1))
	next(PipelineIntervalUpdate)
	if got := <-updates; got != "2s" {
		t.Fatalf("interval=%s", got)
	}
	write(original)
	next(PipelineIntervalUpdate)
	if got := <-updates; got != "1s" {
		t.Fatalf("revert interval=%s", got)
	}
	write(strings.Replace(original, "value + 1", "value + 2", 1))
	select {
	case err := <-done:
		if !errors.Is(err, ErrPipelineMigrationRequired) {
			t.Fatalf("migration=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("migration did not stop watcher")
	}
	if calls.Load() != 2 || current.env.checkpointInterval != time.Second {
		t.Fatal("migration sent a request or mutated caller baseline")
	}
}
