package sdk

import (
	"errors"
	"testing"
)

func TestGraphValidateEmptyPipeline(t *testing.T) {
	g := newStreamGraph()
	err := g.validate()
	if !errors.Is(err, ErrEmptyPipeline) {
		t.Errorf("expected ErrEmptyPipeline, got %v", err)
	}
}

func TestGraphValidateNoSources(t *testing.T) {
	g := newStreamGraph()
	g.addNode(&StreamNode{Name: "sink1", Type: NodeSink})
	err := g.validate()
	if !errors.Is(err, ErrNoSources) {
		t.Errorf("expected ErrNoSources, got %v", err)
	}
}

func TestGraphValidateNoSinks(t *testing.T) {
	g := newStreamGraph()
	g.addNode(&StreamNode{Name: "src1", Type: NodeSource})
	err := g.validate()
	if !errors.Is(err, ErrNoSinks) {
		t.Errorf("expected ErrNoSinks, got %v", err)
	}
}

func TestGraphValidateDuplicateName(t *testing.T) {
	g := newStreamGraph()
	g.addNode(&StreamNode{Name: "op", Type: NodeSource})
	// Manually add second node with same name (bypassing env safeguards).
	g.nodes[g.nextID] = &StreamNode{ID: g.nextID, Name: "op", Type: NodeSink}
	g.nextID++
	err := g.validate()
	if !errors.Is(err, ErrDuplicateName) {
		t.Errorf("expected ErrDuplicateName, got %v", err)
	}
}

func TestGraphValidateCycleDetection(t *testing.T) {
	g := newStreamGraph()
	src := g.addNode(&StreamNode{Name: "src", Type: NodeSource})
	mid := g.addNode(&StreamNode{Name: "mid", Type: NodeMap})
	sink := g.addNode(&StreamNode{Name: "sink", Type: NodeSink})
	g.addEdge(src, mid, ShuffleForward)
	g.addEdge(mid, sink, ShuffleForward)
	g.addEdge(sink, mid, ShuffleForward) // Cycle!

	err := g.validate()
	if !errors.Is(err, ErrCyclicGraph) {
		t.Errorf("expected ErrCyclicGraph, got %v", err)
	}
}

func TestGraphValidateValidPipeline(t *testing.T) {
	g := newStreamGraph()
	src := g.addNode(&StreamNode{Name: "src", Type: NodeSource})
	mapN := g.addNode(&StreamNode{Name: "mapper", Type: NodeMap})
	sink := g.addNode(&StreamNode{Name: "sink", Type: NodeSink})
	g.addEdge(src, mapN, ShuffleForward)
	g.addEdge(mapN, sink, ShuffleForward)

	if err := g.validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestGraphTopoSort(t *testing.T) {
	g := newStreamGraph()
	src := g.addNode(&StreamNode{Name: "src", Type: NodeSource})
	mapN := g.addNode(&StreamNode{Name: "mapper", Type: NodeMap})
	sink := g.addNode(&StreamNode{Name: "sink", Type: NodeSink})
	g.addEdge(src, mapN, ShuffleForward)
	g.addEdge(mapN, sink, ShuffleForward)

	sorted := g.topoSort()
	if len(sorted) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(sorted))
	}

	// Source must come first, sink must come last.
	if sorted[0].Name != "src" {
		t.Errorf("expected first node to be 'src', got %q", sorted[0].Name)
	}
	if sorted[2].Name != "sink" {
		t.Errorf("expected last node to be 'sink', got %q", sorted[2].Name)
	}
}
