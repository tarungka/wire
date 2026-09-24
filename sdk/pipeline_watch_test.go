package sdk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPipelineWatcherRejectsEditsAndDetectsAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline.yaml")
	original := yamlPipelineHeader + "  sinks:\n    - {name: output, type: test-sink, input: input}\n"
	write := func(data string) {
		t.Helper()
		temp := path + ".next"
		if err := os.WriteFile(temp, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, path); err != nil {
			t.Fatal(err)
		}
	}
	write(original)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	applied := make(chan string, 8)
	rejected := make(chan error, 8)
	done := make(chan error, 1)
	bindings := PipelineConnectors{NamedSources: map[string]string{"test-source": "input"}, NamedSinks: map[string]string{"test-sink": "output"}}
	go func() {
		done <- WatchPipelineFile(ctx, path, bindings, PipelineWatchConfig{PollInterval: 5 * time.Millisecond, OnRejected: func(err error) { rejected <- err }}, func(_ context.Context, p *YAMLPipeline) error { applied <- p.Name; return nil })
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("watch ended: %v", err)
		}
	}()
	next := func(want string) {
		t.Helper()
		select {
		case name := <-applied:
			if name != want {
				t.Fatalf("applied %s want %s", name, want)
			}
		case <-ctx.Done():
			t.Fatal("application timeout")
		}
	}
	next("test-pipeline")
	write("invalid: definition")
	select {
	case <-rejected:
	case <-ctx.Done():
		t.Fatal("invalid edit not reported")
	}
	select {
	case name := <-applied:
		t.Fatalf("invalid candidate applied: %s", name)
	default:
	}
	// Equal length and deliberately identical modification time must not conceal
	// a replacement. Detection compares content, not file timestamp/size.
	write(strings.Replace(original, "test-pipeline", "next-pipeline", 1))
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	next("next-pipeline")
	// Reverting to a previous valid version is a new candidate.
	write(original)
	next("test-pipeline")
}

func TestPipelineWatcherStopsOnApplyFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline.yaml")
	data := yamlPipelineHeader + "  sinks:\n    - {name: output, type: test-sink, input: input}\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("migration uncertain")
	calls := 0
	err := WatchPipelineFile(t.Context(), path, PipelineConnectors{NamedSources: map[string]string{"test-source": "input"}, NamedSinks: map[string]string{"test-sink": "output"}}, PipelineWatchConfig{}, func(context.Context, *YAMLPipeline) error { calls++; return sentinel })
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
