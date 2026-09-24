package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/config"
	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/jobcli"
)

func TestNodeWorkerExecutesYAMLHTTPPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	store := coordinator.NewMemoryStore()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "test", WorkerTimeout: 5 * time.Second}, store, nil, zerolog.Nop())
	rpcServer := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	api := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
	var joined sync.WaitGroup
	start := func(fn func()) { joined.Add(1); go func() { defer joined.Done(); fn() }() }
	defer func() {
		cancel()
		stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = api.Shutdown(stopCtx)
		_ = rpcServer.Shutdown(stopCtx)
		joined.Wait()
		_ = store.Close()
	}()
	wait := func(ready func() bool) {
		t.Helper()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatal("runtime timed out")
			case <-time.After(10 * time.Millisecond):
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
	cfg := config.DefaultConfig()
	cfg.Worker.CoordinatorAddr = rpcServer.Addr()
	cfg.Worker.WorkerID = "stock-worker"
	cfg.Worker.ListenAddr = "127.0.0.1:0"
	cfg.Worker.EpochPath = filepath.Join(t.TempDir(), "epoch")
	start(func() {
		if err := runWorker(ctx, &cfg, zerolog.Nop()); err != nil && ctx.Err() == nil {
			t.Error(err)
		}
	})
	wait(func() bool { return len(coord.ListWorkers()) == 1 })
	received := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		select {
		case received <- string(data):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	document := fmt.Sprintf(`apiVersion: wire/v1
kind: Pipeline
metadata: {name: stock-worker-yaml}
spec:
  sources:
    - {name: input, type: http-api, config: {address: %q, allow_insecure: true}}
  transforms:
    - {name: mapped, type: map, input: input, config: {expression: "value + 1"}}
  sinks:
    - {name: output, type: http-api, input: mapped, config: {url: %q, allow_insecure: true}}
`, address, target.URL)
	path := filepath.Join(t.TempDir(), "pipeline.yaml")
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := jobcli.RunWithPipelineCompiler(ctx, []string{"jobs", "submit", "--file", path, "--format", "yaml", "--coordinator", "http://" + api.Addr()}, &output, io.Discard, compileYAMLPipeline); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	wait(func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/ingest", strings.NewReader(`{"events":[{"value":"41"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("ingest status %d", response.StatusCode)
		}
		return true
	})
	select {
	case data := <-received:
		var envelope struct {
			Events []struct {
				Value string `json:"value"`
			} `json:"events"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Events) != 1 || envelope.Events[0].Value != "42" {
			t.Fatalf("output=%s", data)
		}
	case <-ctx.Done():
		t.Fatal("no transformed HTTP output")
	}
}
