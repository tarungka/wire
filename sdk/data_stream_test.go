package sdk

import (
	"context"
	"testing"
	"time"
)

// stubSource is a minimal Source for testing graph construction.
type stubSource struct{}

func (s *stubSource) Open(_ context.Context) error                 { return nil }
func (s *stubSource) ReadBatch(_ context.Context) ([]Event, error) { return nil, nil }
func (s *stubSource) Close() error                                 { return nil }
func (s *stubSource) GenerateWatermark() int64                     { return 0 }

// stubSink is a minimal Sink for testing.
type stubSink struct{}

func (s *stubSink) Open(_ context.Context) error           { return nil }
func (s *stubSink) Write(_ context.Context, _ Event) error { return nil }
func (s *stubSink) Close() error                           { return nil }

func newTestEnv() *StreamExecutionEnvironment {
	return New()
}

func TestDataStreamMap(t *testing.T) {
	env := newTestEnv()
	ds := env.AddSource(&stubSource{})
	mapped := ds.Map(func(e Event) (Event, error) { return e, nil })
	mapped.AddSink(&stubSink{})

	node := env.graph.nodes[mapped.nodeID]
	if node.Type != NodeMap {
		t.Errorf("expected NodeMap, got %v", node.Type)
	}

	// Check edge from source to map is Forward.
	edges := env.graph.downstream(ds.nodeID)
	if len(edges) != 1 {
		t.Fatalf("expected 1 downstream edge, got %d", len(edges))
	}
	if edges[0].Shuffle != ShuffleForward {
		t.Errorf("expected ShuffleForward, got %v", edges[0].Shuffle)
	}
}

func TestDataStreamFlatMap(t *testing.T) {
	env := newTestEnv()
	ds := env.AddSource(&stubSource{})
	flatMapped := ds.FlatMap(func(e Event) ([]Event, error) { return []Event{e}, nil })
	flatMapped.AddSink(&stubSink{})

	node := env.graph.nodes[flatMapped.nodeID]
	if node.Type != NodeFlatMap {
		t.Errorf("expected NodeFlatMap, got %v", node.Type)
	}
}

func TestDataStreamFilter(t *testing.T) {
	env := newTestEnv()
	ds := env.AddSource(&stubSource{})
	filtered := ds.Filter(func(e Event) (bool, error) { return true, nil })
	filtered.AddSink(&stubSink{})

	node := env.graph.nodes[filtered.nodeID]
	if node.Type != NodeFilter {
		t.Errorf("expected NodeFilter, got %v", node.Type)
	}
}

func TestDataStreamKeyBy(t *testing.T) {
	env := newTestEnv()
	ds := env.AddSource(&stubSource{})
	keyed := ds.KeyBy(func(e Event) ([]byte, error) { return e.Key, nil })

	// Check edge is Hash shuffle.
	edges := env.graph.downstream(ds.nodeID)
	if len(edges) != 1 {
		t.Fatalf("expected 1 downstream edge, got %d", len(edges))
	}
	if edges[0].Shuffle != ShuffleHash {
		t.Errorf("expected ShuffleHash, got %v", edges[0].Shuffle)
	}

	node := env.graph.nodes[keyed.nodeID]
	if node.Type != NodeKeyBy {
		t.Errorf("expected NodeKeyBy, got %v", node.Type)
	}
}

func TestDataStreamChaining(t *testing.T) {
	env := newTestEnv()
	env.AddSource(&stubSource{}).
		Map(func(e Event) (Event, error) { return e, nil }).
		Filter(func(e Event) (bool, error) { return true, nil }).
		FlatMap(func(e Event) ([]Event, error) { return []Event{e}, nil }).
		AddSink(&stubSink{})

	// Graph should have: source, map, filter, flatmap, sink = 5 nodes.
	if len(env.graph.nodes) != 5 {
		t.Errorf("expected 5 nodes, got %d", len(env.graph.nodes))
	}
	// 4 edges: source→map, map→filter, filter→flatmap, flatmap→sink.
	if len(env.graph.edges) != 4 {
		t.Errorf("expected 4 edges, got %d", len(env.graph.edges))
	}
}

func TestDataStreamSetParallelism(t *testing.T) {
	env := newTestEnv()
	ds := env.AddSource(&stubSource{}).SetParallelism(8)
	node := env.graph.nodes[ds.nodeID]
	if node.Parallelism != 8 {
		t.Errorf("expected parallelism=8, got %d", node.Parallelism)
	}
}

func TestKeyedStreamProcess(t *testing.T) {
	env := newTestEnv()
	ds := env.AddSource(&stubSource{}).
		KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).
		Process(func(ctx ProcessContext, e Event) ([]Event, error) { return []Event{e}, nil })

	node := env.graph.nodes[ds.nodeID]
	if node.Type != NodeProcess {
		t.Errorf("expected NodeProcess, got %v", node.Type)
	}
}

func TestWindowedStreamAggregate(t *testing.T) {
	env := newTestEnv()
	ds := env.AddSource(&stubSource{}).
		KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).
		Window(TumblingWindow(5 * time.Second)).
		Aggregate(CountAggregator{})

	node := env.graph.nodes[ds.nodeID]
	if node.Type != NodeWindow {
		t.Errorf("expected NodeWindow, got %v", node.Type)
	}
	if node.Window == nil {
		t.Error("expected Window assigner to be set")
	}
}

func TestWindowedStreamReduce(t *testing.T) {
	env := newTestEnv()
	ds := env.AddSource(&stubSource{}).
		KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).
		Window(SlidingWindow(10*time.Second, 5*time.Second)).
		Reduce(func(a, b Event) (Event, error) { return a, nil })

	node := env.graph.nodes[ds.nodeID]
	if node.Type != NodeReduce {
		t.Errorf("expected NodeReduce, got %v", node.Type)
	}
}
