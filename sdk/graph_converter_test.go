package sdk

import (
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestToJobGraphBasic(t *testing.T) {
	g := newStreamGraph()
	src := g.addNode(&StreamNode{Name: "src", Type: NodeSource, Parallelism: 2})
	mapN := g.addNode(&StreamNode{Name: "mapper", Type: NodeMap})
	sink := g.addNode(&StreamNode{Name: "sink", Type: NodeSink})
	g.addEdge(src, mapN, ShuffleForward)
	g.addEdge(mapN, sink, ShuffleForward)

	jg := g.toJobGraph(4)

	if len(jg.Operators) != 3 {
		t.Fatalf("expected 3 operators, got %d", len(jg.Operators))
	}
	if len(jg.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(jg.Edges))
	}

	// Check operator types.
	typeMap := make(map[string]rpc.OperatorType)
	parallelismMap := make(map[string]int32)
	for _, op := range jg.Operators {
		typeMap[op.OperatorID] = op.Type
		parallelismMap[op.OperatorID] = op.Parallelism
	}

	if typeMap["src"] != rpc.OperatorTypeSource {
		t.Errorf("expected Source type, got %v", typeMap["src"])
	}
	if typeMap["mapper"] != rpc.OperatorTypeMap {
		t.Errorf("expected Map type, got %v", typeMap["mapper"])
	}
	if typeMap["sink"] != rpc.OperatorTypeSink {
		t.Errorf("expected Sink type, got %v", typeMap["sink"])
	}

	// Source should use explicit parallelism (2), mapper should inherit default (4).
	if parallelismMap["src"] != 2 {
		t.Errorf("expected source parallelism=2, got %d", parallelismMap["src"])
	}
	if parallelismMap["mapper"] != 4 {
		t.Errorf("expected mapper parallelism=4 (default), got %d", parallelismMap["mapper"])
	}

	// Check edges.
	for _, edge := range jg.Edges {
		if edge.Shuffle != rpc.ShuffleStrategyForward {
			t.Errorf("expected Forward shuffle, got %v", edge.Shuffle)
		}
	}
}

func TestToJobGraphHashShuffle(t *testing.T) {
	g := newStreamGraph()
	src := g.addNode(&StreamNode{Name: "src", Type: NodeSource})
	keyby := g.addNode(&StreamNode{Name: "keyby", Type: NodeKeyBy})
	sink := g.addNode(&StreamNode{Name: "sink", Type: NodeSink})
	g.addEdge(src, keyby, ShuffleHash)
	g.addEdge(keyby, sink, ShuffleForward)

	jg := g.toJobGraph(1)

	// Find the hash edge.
	found := false
	for _, edge := range jg.Edges {
		if edge.SourceOperatorID == "src" && edge.TargetOperatorID == "keyby" {
			if edge.Shuffle != rpc.ShuffleStrategyHash {
				t.Errorf("expected Hash shuffle, got %v", edge.Shuffle)
			}
			found = true
		}
	}
	if !found {
		t.Error("hash edge not found")
	}
}
