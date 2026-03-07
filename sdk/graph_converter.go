package sdk

import "github.com/tarungka/wire/internal/rpc"

// toJobGraph converts the StreamGraph to an rpc.JobGraph suitable for
// cluster submission.
func (g *StreamGraph) toJobGraph(defaultParallelism int) rpc.JobGraph {
	var ops []rpc.OperatorDescriptor
	var edges []rpc.EdgeDescriptor

	// Map node IDs to string operator IDs.
	idStr := func(id int) string {
		node := g.nodes[id]
		if node.Name != "" {
			return node.Name
		}
		return nodeTypeString(node.Type) + "-" + itoa(id)
	}

	for _, node := range g.topoSort() {
		p := node.Parallelism
		if p <= 0 {
			p = defaultParallelism
		}

		ops = append(ops, rpc.OperatorDescriptor{
			OperatorID:  idStr(node.ID),
			Name:        node.Name,
			Type:        nodeTypeToRPC(node.Type),
			Parallelism: int32(p),
		})
	}

	for _, edge := range g.edges {
		edges = append(edges, rpc.EdgeDescriptor{
			SourceOperatorID: idStr(edge.SourceID),
			TargetOperatorID: idStr(edge.TargetID),
			Shuffle:          shuffleTypeToRPC(edge.Shuffle),
		})
	}

	return rpc.JobGraph{
		Operators: ops,
		Edges:     edges,
	}
}

func nodeTypeToRPC(t StreamNodeType) rpc.OperatorType {
	switch t {
	case NodeSource:
		return rpc.OperatorTypeSource
	case NodeMap:
		return rpc.OperatorTypeMap
	case NodeFlatMap:
		return rpc.OperatorTypeFlatMap
	case NodeFilter:
		return rpc.OperatorTypeFilter
	case NodeKeyBy:
		return rpc.OperatorTypeKeyBy
	case NodeWindow:
		return rpc.OperatorTypeWindow
	case NodeReduce:
		return rpc.OperatorTypeReduce
	case NodeProcess:
		return rpc.OperatorTypeProcess
	case NodeSink:
		return rpc.OperatorTypeSink
	default:
		return rpc.OperatorTypeUnknown
	}
}

func shuffleTypeToRPC(s ShuffleType) rpc.ShuffleStrategy {
	switch s {
	case ShuffleForward:
		return rpc.ShuffleStrategyForward
	case ShuffleHash:
		return rpc.ShuffleStrategyHash
	case ShuffleBroadcast:
		return rpc.ShuffleStrategyBroadcast
	case ShuffleRebalance:
		return rpc.ShuffleStrategyRebalance
	default:
		return rpc.ShuffleStrategyUnknown
	}
}

func nodeTypeString(t StreamNodeType) string {
	switch t {
	case NodeSource:
		return "source"
	case NodeMap:
		return "map"
	case NodeFlatMap:
		return "flatmap"
	case NodeFilter:
		return "filter"
	case NodeKeyBy:
		return "keyby"
	case NodeWindow:
		return "window"
	case NodeReduce:
		return "reduce"
	case NodeProcess:
		return "process"
	case NodeSink:
		return "sink"
	default:
		return "unknown"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// Reverse.
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
