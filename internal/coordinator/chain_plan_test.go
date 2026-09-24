package coordinator

import (
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestPhysicalChainsSplitAtShuffleAndParallelism(t *testing.T) {
	graph := linearGraph()
	chains, membership, err := planPhysicalChains(graph, 3)
	if err != nil || len(chains) != 1 || len(chains[0].Operators) != 3 || membership["src"] != membership["snk"] {
		t.Fatalf("linear: %v %v", chains, err)
	}
	graph.Edges[0].Shuffle = rpc.ShuffleStrategyHash
	chains, membership, err = planPhysicalChains(graph, 3)
	if err != nil || len(chains) != 2 || membership["src"] == membership["m"] || membership["m"] != membership["snk"] {
		t.Fatalf("shuffle: %v %v %v", chains, membership, err)
	}
	graph.Operators[2].Parallelism = 2
	chains, membership, err = planPhysicalChains(graph, 3)
	if err != nil || len(chains) != 3 || chains[membership["snk"]].Parallelism != 2 {
		t.Fatalf("parallelism: %v %v", chains, err)
	}
}

func TestPhysicalChainsPreserveBranchBoundaries(t *testing.T) {
	graph := linearGraph()
	graph.Operators = append(graph.Operators, rpc.OperatorDescriptor{OperatorID: "other", Type: rpc.OperatorTypeSink})
	graph.Edges = append(graph.Edges, rpc.EdgeDescriptor{SourceOperatorID: "src", TargetOperatorID: "other", Shuffle: rpc.ShuffleStrategyForward})
	chains, membership, err := planPhysicalChains(graph, 2)
	if err != nil || len(chains) != 3 || membership["src"] == membership["m"] || membership["src"] == membership["other"] {
		t.Fatalf("branch: %v %v %v", chains, membership, err)
	}
}

func TestPhysicalChainsLegacyOmittedShuffleIsForward(t *testing.T) {
	graph := linearGraph()
	for i := range graph.Edges {
		graph.Edges[i].Shuffle = rpc.ShuffleStrategyUnknown
	}
	tasks, err := buildPhysicalTasks("legacy", graph, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || len(tasks[0].OperatorChain) != 3 || len(tasks[0].Downstream) != 0 {
		t.Fatalf("legacy graph no longer fused: %+v", tasks)
	}
}
