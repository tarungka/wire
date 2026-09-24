package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type watchOutput chan string

func (w watchOutput) Write(data []byte) (int, error) { w <- string(data); return len(data), nil }

func TestCLIWatchAppliesIntervalEdit(t *testing.T) {
	updates := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/jobs/job/checkpoint-interval" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		updates <- struct{}{}
		_, _ = io.WriteString(w, `{"id":"job","checkpoint_interval":"2s"}`)
	}))
	defer server.Close()
	definition := strings.Replace(cliYAML, "spec:", "spec:\n  checkpoint: {interval: 1s}", 1)
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
	write(definition)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	output := make(watchOutput, 4)
	done := make(chan error, 1)
	go func() {
		done <- runPipelineWatch(ctx, []string{"job", "--file", path, "--coordinator", server.URL, "--poll-interval", "5ms"}, output, io.Discard)
	}()
	select {
	case line := <-output:
		if !strings.Contains(line, "unchanged") {
			t.Fatalf("initial=%s", line)
		}
	case <-ctx.Done():
		t.Fatal("watch not started")
	}
	write(strings.Replace(definition, "interval: 1s", "interval: 2s", 1))
	select {
	case <-updates:
	case <-ctx.Done():
		t.Fatal("no live update")
	}
	select {
	case line := <-output:
		if !strings.Contains(line, "checkpoint-interval") {
			t.Fatalf("update=%s", line)
		}
	case <-ctx.Done():
		t.Fatal("no confirmation")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown=%v", err)
	}
}

func TestCLIWatchRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"job", "--poll-interval", "0s"}, {"../job", "--file", "unused"}, {"job", "--file", "unused", "--coordinator", "invalid"}} {
		if err := runPipelineWatch(t.Context(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
