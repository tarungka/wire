package sdk

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
)

func TestPublicWorkerRegistryExecutesRemoteGraph(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	store := coordinator.NewMemoryStore()
	defer store.Close()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "sdk-coordinator"}, store, nil, zerolog.Nop())
	var joined sync.WaitGroup
	start := func(fn func()) { joined.Add(1); go func() { defer joined.Done(); fn() }() }
	start(func() { _ = coord.Run(ctx) })
	defer func() { cancel(); joined.Wait() }()
	wait := func(ready func() bool) {
		t.Helper()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatal("runtime readiness timeout")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	wait(coord.IsReady)
	rpcServer := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := rpcServer.Listen(); err != nil {
		t.Fatal(err)
	}
	defer rpcServer.Shutdown(context.Background())
	start(func() { _ = rpcServer.Serve(ctx) })
	httpServer := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := httpServer.Listen(); err != nil {
		t.Fatal(err)
	}
	defer httpServer.Shutdown(context.Background())
	start(func() { _ = httpServer.Serve() })
	registry := NewWorkerRegistry()
	contexts := make(chan WorkerTaskContext, 1)
	registry.RegisterSource("source", func(_ context.Context, config []byte, tc WorkerTaskContext) (Source, error) {
		if string(config) != "application-config" {
			return nil, fmt.Errorf("lost source configuration")
		}
		contexts <- tc
		return &sliceSource{events: []Event{{Key: []byte("k"), Value: []byte("one"), EventTime: 1}, {Key: []byte("k"), Value: []byte("two"), EventTime: 2}}}, nil
	})
	registry.RegisterMap("map", func(context.Context, []byte, WorkerTaskContext) (MapFunc, error) {
		return func(e Event) (Event, error) { return e, nil }, nil
	})
	registry.RegisterFlatMap("flat", func(context.Context, []byte, WorkerTaskContext) (FlatMapFunc, error) {
		return func(e Event) ([]Event, error) { return []Event{e}, nil }, nil
	})
	registry.RegisterFilter("filter", func(context.Context, []byte, WorkerTaskContext) (FilterFunc, error) {
		return func(Event) (bool, error) { return true, nil }, nil
	})
	registry.RegisterKeyBy("key", func(context.Context, []byte, WorkerTaskContext) (KeySelector, error) {
		return func(e Event) ([]byte, error) { return e.Key, nil }, nil
	})
	registry.RegisterProcess("process", func(context.Context, []byte, WorkerTaskContext) (ProcessDefinition, error) {
		return ProcessDefinition{Process: func(c ProcessContext, e Event) ([]Event, error) {
			n, err := c.GetState("count").ValueInt64()
			if err != nil {
				return nil, err
			}
			if err := c.GetState("count").SetInt64(n + 1); err != nil {
				return nil, err
			}
			e.Value = []byte(fmt.Sprint(n + 1))
			return []Event{e}, nil
		}}, nil
	})
	registry.RegisterWindow("window", func(context.Context, []byte, WorkerTaskContext) (WindowDefinition, error) {
		return WindowDefinition{Aggregator: CountAggregator{}}, nil
	})
	mainSink, windowSink := &collectSink{}, &collectSink{}
	registry.RegisterSink("sink", func(_ context.Context, config []byte, _ WorkerTaskContext) (Sink, error) {
		if string(config) == "window" {
			return windowSink, nil
		}
		return mainSink, nil
	})
	for i := 0; i < 2; i++ {
		cfg := WorkerConfig{WorkerID: fmt.Sprint("sdk-worker-", i), CoordinatorAddr: rpcServer.Addr(), TaskSlots: 8, HeartbeatInterval: 100 * time.Millisecond, CheckpointDirectory: t.TempDir()}
		start(func() {
			if err := RunWorker(ctx, cfg, registry); err != nil && ctx.Err() == nil {
				t.Error(err)
			}
		})
	}
	wait(func() bool { return len(coord.ListWorkers()) == 2 })
	env := NewStreamExecutionEnvironment().SetMode(Cluster).SetCoordinator("http://" + httpServer.Addr()).SetParallelism(2).SetStateBackend(NewHashMapStateBackend(4))
	keyed := env.AddSourceNamed("source", "source", []byte("application-config")).SetParallelism(1).MapNamed("map", "map", nil).FlatMapNamed("flat", "flat", nil).FilterNamed("filter", "filter", nil).KeyByNamed("key", "key", nil)
	keyed.ProcessNamed("process", "process", nil).AddSinkNamed("main", "sink", nil).SetParallelism(1)
	keyed.Window(TumblingWindow(10*time.Millisecond)).ApplyNamed("window", "window", nil).AddSinkNamed("window-sink", "sink", []byte("window")).SetParallelism(1)
	if _, err := env.ExecuteWithName(ctx, "public-sdk"); err != nil {
		t.Fatal(err)
	}
	if events := mainSink.Events(); len(events) != 2 || string(events[0].Value) != "1" || string(events[1].Value) != "2" {
		t.Fatalf("managed Process output: %+v", events)
	}
	if events := windowSink.Events(); len(events) != 1 {
		t.Fatalf("window output: %+v", events)
	}
	identity := <-contexts
	if identity.JobID == "" || identity.TaskID == "" || identity.OperatorID != "source" || identity.AttemptID == "" || identity.Parallelism != 1 || identity.EpochID == 0 || identity.DeploymentGeneration == 0 {
		t.Fatalf("factory identity incomplete: %+v", identity)
	}
}

func TestRunWorkerRejectsInvalidConfiguration(t *testing.T) {
	for _, config := range []WorkerConfig{{}, {CoordinatorAddr: "127.0.0.1:1", TaskSlots: -1}, {CoordinatorAddr: "127.0.0.1:1", CheckpointConcurrency: -1}, {CoordinatorAddr: "127.0.0.1:1", ShutdownTimeout: -1}} {
		if err := RunWorker(t.Context(), config, NewWorkerRegistry()); err == nil {
			t.Fatal("invalid worker configuration accepted")
		}
	}
	if err := RunWorker(t.Context(), WorkerConfig{CoordinatorAddr: "127.0.0.1:1"}, nil); err == nil {
		t.Fatal("nil registry accepted")
	}
}
