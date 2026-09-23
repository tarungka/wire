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
		want := rpc.ShuffleStrategyForward
		if edge.SourceOperatorID == "src" {
			want = rpc.ShuffleStrategyRebalance
		}
		if edge.Shuffle != want {
			t.Errorf("edge %s: expected %v, got %v", edge.SourceOperatorID, want, edge.Shuffle)
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
		if edge.SourceOperatorID == "keyby" && edge.TargetOperatorID == "sink" {
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

func TestNamedKeyBySelectsBeforeShuffle(t *testing.T) {
	env := New()
	env.AddSourceNamed("source", "source-factory", nil).KeyByNamed("select", "selector-factory", []byte("cfg")).AddSinkNamed("sink", "sink-factory", nil)
	if err := env.graph.validateForCluster(); err != nil {
		t.Fatal(err)
	}
	graph := env.graph.toJobGraph(3)
	if len(graph.Edges) != 2 || graph.Edges[0].Shuffle != rpc.ShuffleStrategyForward || graph.Edges[1].Shuffle != rpc.ShuffleStrategyHash {
		t.Fatalf("edges=%+v", graph.Edges)
	}
	if graph.Operators[1].ClassName != "selector-factory" || string(graph.Operators[1].Config) != "cfg" {
		t.Fatalf("selector=%+v", graph.Operators[1])
	}
}

func TestKeyByInheritsSingleSourceParallelism(t *testing.T) {
	env := New().SetParallelism(4)
	env.AddSourceNamed("source", "source", nil).SetParallelism(1).KeyByNamed("key", "selector", nil).AddSinkNamed("sink", "sink", nil)
	graph := env.graph.toJobGraph(4)
	if graph.Operators[0].Parallelism != 1 || graph.Operators[1].Parallelism != 1 || graph.Operators[2].Parallelism != 4 {
		t.Fatalf("parallelism: %+v", graph.Operators)
	}
	if graph.Edges[0].Shuffle != rpc.ShuffleStrategyForward || graph.Edges[1].Shuffle != rpc.ShuffleStrategyHash {
		t.Fatalf("routing: %+v", graph.Edges)
	}
}

func TestKeyByExplicitAndMixedInputParallelism(t *testing.T) {
	for _, explicit := range []int{0, 3} {
		g := newStreamGraph()
		a := g.addNode(&StreamNode{Name: "a", Type: NodeSource, Parallelism: 1})
		b := g.addNode(&StreamNode{Name: "b", Type: NodeSource, Parallelism: 4})
		k := g.addNode(&StreamNode{Name: "key", Type: NodeKeyBy, Parallelism: explicit})
		g.addEdge(a, k, ShuffleHash)
		g.addEdge(b, k, ShuffleHash)
		graph := g.toJobGraph(4)
		counts := map[string]int32{}
		for _, op := range graph.Operators {
			counts[op.OperatorID] = op.Parallelism
		}
		if explicit > 0 && counts["key"] != int32(explicit) {
			t.Fatal("explicit KeyBy count ignored")
		}
		for _, edge := range graph.Edges {
			want := rpc.ShuffleStrategyForward
			if counts[edge.SourceOperatorID] != counts[edge.TargetOperatorID] {
				want = rpc.ShuffleStrategyRebalance
			}
			if edge.Shuffle != want {
				t.Fatalf("invalid pre-key routing: %+v", edge)
			}
		}
	}
}

func TestNamedProcessPreservesSideOutputDeclarations(t *testing.T) {
	env := New()
	process := env.AddSourceNamed("source", "source", nil).KeyByNamed("keys", "selector", nil).ProcessNamed("process", "managed", []byte("config")).WithSideOutputs(NewOutputTag("audit"))
	process.AddSinkNamed("main", "sink", nil)
	process.GetSideOutput(NewOutputTag("audit")).AddSinkNamed("audit", "sink", nil)
	if err := env.graph.validateForCluster(); err != nil {
		t.Fatal(err)
	}
	graph := env.graph.toJobGraph(2)
	found := false
	for _, op := range graph.Operators {
		if op.OperatorID == "process" {
			found = true
			if op.Type != rpc.OperatorTypeProcess || op.ClassName != "managed" || string(op.Config) != "config" || len(op.SideOutputTags) != 1 || op.SideOutputTags[0] != "audit" {
				t.Fatalf("operator=%+v", op)
			}
		}
	}
	if !found {
		t.Fatal("Process disappeared from graph")
	}
}

func TestExplicitOperatorParallelismRedistributesForwardEdges(t *testing.T) {
	env := New().SetParallelism(4)
	env.AddSourceNamed("source", "source", nil).SetParallelism(1).MapNamed("map", "map", nil).AddSinkNamed("sink", "sink", nil).SetParallelism(2)
	graph := env.graph.toJobGraph(4)
	for _, edge := range graph.Edges {
		if edge.Shuffle != rpc.ShuffleStrategyRebalance {
			t.Fatalf("unequal forward edge was not redistributed: %+v", edge)
		}
	}
}
