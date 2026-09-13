package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/rpc"
)

// buildPhysicalTasks materializes chains and their network boundaries. Addresses
// are filled after worker placement; endpoint order follows target subtask order.
func buildPhysicalTasks(jobID string, graph rpc.JobGraph, parallelism int) ([]rpc.TaskDescriptor, error) {
	count, err := validateGraphKeyGroups(graph, parallelism)
	if err != nil {
		return nil, err
	}
	chains, membership, err := planPhysicalChains(graph, parallelism)
	if err != nil {
		return nil, err
	}
	var tasks []rpc.TaskDescriptor
	indexes := make([][]int, len(chains))
	for ci, chain := range chains {
		ranges, err := keygroup.AllTaskRanges(count, chain.Parallelism)
		if err != nil {
			return nil, err
		}
		primary := chain.Operators[0].OperatorID
		for _, op := range chain.Operators {
			if op.Type != rpc.OperatorTypeSource {
				primary = op.OperatorID
				break
			}
		}
		for i, groups := range ranges {
			indexes[ci] = append(indexes[ci], len(tasks))
			tasks = append(tasks, rpc.TaskDescriptor{TaskID: fmt.Sprintf("%s/%s/%d", jobID, primary, i), OperatorID: primary, SubtaskIndex: int32(i), Parallelism: int32(chain.Parallelism), NumKeyGroups: count, KeyGroup: rpc.KeyGroupRange{Start: int32(groups.Start), End: int32(groups.End) - 1}, OperatorChain: chain.Operators})
		}
	}
	boundaryCount := make(map[int]int)
	for _, edge := range graph.Edges {
		// Omitted shuffle strategy in persisted legacy graphs means forward.
		if edge.Shuffle == rpc.ShuffleStrategyUnknown {
			edge.Shuffle = rpc.ShuffleStrategyForward
		}
		source, target := membership[edge.SourceOperatorID], membership[edge.TargetOperatorID]
		if source == target {
			continue
		}
		boundaryCount[source]++
		if boundaryCount[source] > 1 {
			return nil, fmt.Errorf("multiple output edges require grouped routing")
		}
		if edge.Shuffle != rpc.ShuffleStrategyForward && edge.Shuffle != rpc.ShuffleStrategyHash && edge.Shuffle != rpc.ShuffleStrategyRebalance {
			return nil, fmt.Errorf("unsupported shuffle strategy %v", edge.Shuffle)
		}
		if edge.KeySelector != "" {
			return nil, fmt.Errorf("edge key selector must be executed by an upstream KeyBy operator")
		}
		if edge.Shuffle == rpc.ShuffleStrategyHash {
			for _, src := range indexes[source] {
				tasks[src].OutputKeyGroups = count
			}
		}
		if edge.Shuffle == rpc.ShuffleStrategyForward && len(indexes[source]) != len(indexes[target]) {
			return nil, fmt.Errorf("forward edge %s→%s requires equal parallelism", edge.SourceOperatorID, edge.TargetOperatorID)
		}
		for si, src := range indexes[source] {
			for ti, dst := range indexes[target] {
				if edge.Shuffle == rpc.ShuffleStrategyForward && si != ti {
					continue
				}
				if len(tasks[dst].Upstream) >= 65536 {
					return nil, fmt.Errorf("task %s exceeds stream partition limit", tasks[dst].TaskID)
				}
				partition := uint16(len(tasks[dst].Upstream))
				tasks[src].Downstream = append(tasks[src].Downstream, rpc.DownstreamChannelInfo{TaskID: tasks[dst].TaskID, OperatorID: tasks[dst].OperatorID, SubtaskIndex: int32(ti), PartitionIndex: partition})
				tasks[dst].Upstream = append(tasks[dst].Upstream, rpc.UpstreamChannelInfo{TaskID: tasks[src].TaskID, OperatorID: tasks[src].OperatorID, SubtaskIndex: int32(si), PartitionIndex: partition})
			}
		}
	}
	return tasks, nil
}
