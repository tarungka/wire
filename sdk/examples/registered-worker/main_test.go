package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/jobcli"
)

// Exercise the same paths the two documented commands call, including network
// submission, named factory lookup, bounded completion and worker shutdown.
func TestRegisteredWorkerExample(t *testing.T)         { testRegisteredWorkerExample(t, false) }
func TestExportedRegisteredWorkerExample(t *testing.T) { testRegisteredWorkerExample(t, true) }
func testRegisteredWorkerExample(t *testing.T, export bool) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	store := coordinator.NewMemoryStore()
	defer store.Close()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "example-coordinator"}, store, nil, zerolog.Nop())
	var joined sync.WaitGroup
	start := func(fn func()) { joined.Add(1); go func() { defer joined.Done(); fn() }() }
	start(func() { _ = coord.Run(ctx) })
	defer func() { cancel(); joined.Wait() }()
	wait := func(ready func() bool) {
		t.Helper()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatal("readiness timeout")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	wait(coord.IsReady)
	rpc := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := rpc.Listen(); err != nil {
		t.Fatal(err)
	}
	defer rpc.Shutdown(context.Background())
	start(func() { _ = rpc.Serve(ctx) })
	http := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := http.Listen(); err != nil {
		t.Fatal(err)
	}
	defer http.Shutdown(context.Background())
	start(func() { _ = http.Serve() })
	var output bytes.Buffer
	stopped := make(chan error, 1)
	start(func() { stopped <- run(ctx, "worker", rpc.Addr(), "", &output) })
	wait(func() bool { return len(coord.ListWorkers()) == 1 })
	if export {
		var payload bytes.Buffer
		if err := run(ctx, "export", "", "", &payload); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "submission.json")
		if err := os.WriteFile(path, payload.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if err := jobcli.Run(ctx, []string{"jobs", "submit", "--file", path, "--coordinator", "http://" + http.Addr()}, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		wait(func() bool {
			jobs := coord.ListJobs(nil)
			if len(jobs) == 1 && jobs[0].Status == coordinator.JobFailed {
				t.Fatal("exported graph failed")
			}
			return len(jobs) == 1 && jobs[0].Status == coordinator.JobFinished
		})
	} else if err := run(ctx, "submit", "", "http://"+http.Addr(), io.Discard); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not join shutdown")
	}
	if got := output.String(); got != "HELLO\nWORLD\n" {
		t.Fatalf("output = %q", got)
	}
}
