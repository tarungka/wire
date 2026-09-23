package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/rpc"
)

// physicalChain fuses only a one-to-one forward path at equal parallelism.
// Fan-out, fan-in and shuffle edges remain explicit network boundaries.
type physicalChain struct {
	Operators   []rpc.OperatorDescriptor
	Parallelism int
}

func planPhysicalChains(graph rpc.JobGraph, defaultParallelism int) ([]physicalChain, map[string]int, error) {
	sorted, err := topoSortOperators(graph)
	if err != nil {
		return nil, nil, err
	}
	incoming := make(map[string][]rpc.EdgeDescriptor)
	outgoing := make(map[string][]rpc.EdgeDescriptor)
	for _, edge := range graph.Edges {
		// Omitted shuffle strategy in persisted legacy graphs means forward.
		if edge.Shuffle == rpc.ShuffleStrategyUnknown {
			edge.Shuffle = rpc.ShuffleStrategyForward
		}
		incoming[edge.TargetOperatorID] = append(incoming[edge.TargetOperatorID], edge)
		outgoing[edge.SourceOperatorID] = append(outgoing[edge.SourceOperatorID], edge)
	}
	var chains []physicalChain
	membership := make(map[string]int)
	for _, op := range sorted {
		if _, exists := membership[op.OperatorID]; exists {
			return nil, nil, fmt.Errorf("duplicate operator %q", op.OperatorID)
		}
		p := int(op.Parallelism)
		if p == 0 {
			p = defaultParallelism
		}
		if p < 1 {
			return nil, nil, fmt.Errorf("operator %q has invalid parallelism", op.OperatorID)
		}
		chainIndex := -1
		if edges := incoming[op.OperatorID]; len(edges) == 1 {
			edge := edges[0]
			parent, exists := membership[edge.SourceOperatorID]
			if exists && edge.SideOutput == "" && edge.Shuffle == rpc.ShuffleStrategyForward && len(outgoing[edge.SourceOperatorID]) == 1 && chains[parent].Parallelism == p {
				chainIndex = parent
			}
		}
		if chainIndex < 0 {
			chainIndex = len(chains)
			chains = append(chains, physicalChain{Parallelism: p})
		}
		chains[chainIndex].Operators = append(chains[chainIndex].Operators, op)
		membership[op.OperatorID] = chainIndex
	}
	return chains, membership, nil
}
