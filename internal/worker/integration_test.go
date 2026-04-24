package worker_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/worker"
	"github.com/tarungka/wire/sdk"
	"github.com/tarungka/wire/sdk/connectors/memory"
)

// TestClusterExecution_LinearPipeline verifies that an SDK-built linear
// pipeline (Source → Map → Sink) submitted in Cluster mode flows through a
// coordinator and a live worker process, and that the sink captures the
// expected mapped events.
//
// This is the Phase 1 MVP end-to-end test: it proves that the worker
// actually instantiates operators and processes data, not just reports
// Running status.
func TestClusterExecution_LinearPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// --- Boot coordinator ---

	store := coordinator.NewMemoryStore()
	coord := coordinator.New(coordinator.CoordinatorConfig{
		NodeID:     "test-coord",
		ListenAddr: "127.0.0.1:0",
	}, store, nil /* single-node */, zerolog.Nop())

	coordDone := make(chan error, 1)
	go func() { coordDone <- coord.Run(ctx) }()

	waitFor(t, 2*time.Second, func() bool { return coord.IsReady() })

	// --- Transport server (worker RPC) ---

	transportSrv := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := transportSrv.Listen(); err != nil {
		t.Fatalf("transport Listen: %v", err)
	}
	transportDone := make(chan error, 1)
	go func() { transportDone <- transportSrv.Serve(ctx) }()
	defer transportSrv.Shutdown(context.Background())

	// --- HTTP server ---

	httpSrv := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := httpSrv.Listen(); err != nil {
		t.Fatalf("http Listen: %v", err)
	}
	httpDone := make(chan error, 1)
	go func() { httpDone <- httpSrv.Serve() }()
	defer httpSrv.Shutdown(context.Background())

	// --- Registry + worker ---

	// Use a fresh sinkID so parallel test runs don't collide.
	sinkID := fmt.Sprintf("test-%d", time.Now().UnixNano())
	defer memory.Reset(sinkID)

	reg := worker.NewRegistry()
	reg.RegisterSource("memory-source", memory.SourceFactory())
	reg.RegisterMap("upper", upperMapFactory())
	reg.RegisterSink("memory-sink", memory.SinkFactory())

	w := worker.NewWithRegistry(worker.Config{
		WorkerID:        "test-worker",
		CoordinatorAddr: transportSrv.Addr(),
		TaskSlots:       4,
	}, reg, zerolog.Nop())
	workerDone := make(chan error, 1)
	go func() { workerDone <- w.Run(ctx) }()
	defer w.Shutdown(context.Background())

	// Wait for the worker to register.
	waitFor(t, 3*time.Second, func() bool {
		workers := coord.ListWorkers()
		return len(workers) == 1 && workers[0].ID == "test-worker"
	})

	// --- Build and submit an SDK pipeline in Cluster mode ---

	events := [][]byte{[]byte("hello"), []byte("world"), []byte("wire")}
	sourceCfg := mustMsgpack(t, memory.SourceConfig{Events: events})
	sinkCfg := mustMsgpack(t, memory.SinkConfig{SinkID: sinkID})

	env := sdk.New().
		SetMode(sdk.Cluster).
		SetCoordinator("http://" + httpSrv.Addr()).
		SetParallelism(1)

	env.AddSourceNamed("src", "memory-source", sourceCfg).
		MapNamed("upper", "upper", nil).
		AddSinkNamed("sink", "memory-sink", sinkCfg)

	res, err := env.ExecuteWithName(ctx, "phase1-mvp")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("job err: %v", res.Err)
	}

	// --- Assert the sink captured the expected events ---

	got := memory.Collected(sinkID)
	wantValues := []string{"HELLO", "WORLD", "WIRE"}
	if len(got) != len(wantValues) {
		t.Fatalf("collected %d events, want %d; got=%v", len(got), len(wantValues), valuesOf(got))
	}
	gotVals := valuesOf(got)
	if !equalStrings(gotVals, wantValues) {
		t.Fatalf("collected values mismatch: got=%v want=%v", gotVals, wantValues)
	}
}

// upperMapFactory returns a MapFactory whose operator uppercases the Value
// of each event. It is the user-defined function for this test.
func upperMapFactory() worker.MapFactory {
	return func(_ context.Context, _ []byte, _ worker.TaskContext) (engine.MapOperator, error) {
		return &upperMap{}, nil
	}
}

type upperMap struct{}

func (*upperMap) Open(_ context.Context) error        { return nil }
func (*upperMap) Close() error                        { return nil }
func (*upperMap) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (*upperMap) Map(_ context.Context, e engine.Event) (engine.Event, error) {
	e.Value = []byte(strings.ToUpper(string(e.Value)))
	return e, nil
}

var _ engine.MapOperator = (*upperMap)(nil)

// --- Helpers ---

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not satisfied within %s", timeout)
}

func mustMsgpack(t *testing.T, v any) []byte {
	t.Helper()
	b, err := protocol.EncodeMsgPack(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

func valuesOf(events []engine.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e.Value)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	// Order doesn't matter within a single subtask (Phase 1 parallelism=1
	// preserves order, but be resilient).
	am := make(map[string]int)
	for _, s := range a {
		am[s]++
	}
	for _, s := range b {
		am[s]--
	}
	for _, n := range am {
		if n != 0 {
			return false
		}
	}
	return true
}

