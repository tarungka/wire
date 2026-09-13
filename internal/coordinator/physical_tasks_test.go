package coordinator

import (
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestPhysicalTasksHashShuffleEndpoints(t *testing.T) {
	graph := linearGraph()
	graph.NumKeyGroups = 16
	graph.Edges[0].Shuffle = rpc.ShuffleStrategyHash
	graph.Operators[0].Parallelism = 2
	tasks, err := buildPhysicalTasks("job", graph, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 5 {
		t.Fatalf("tasks=%d", len(tasks))
	}
	byID := make(map[string]rpc.TaskDescriptor)
	for _, task := range tasks {
		byID[task.TaskID] = task
	}
	for _, source := range tasks[:2] {
		if len(source.Downstream) != 3 || len(source.Upstream) != 0 {
			t.Fatalf("source=%+v", source)
		}
		for index, down := range source.Downstream {
			if down.SubtaskIndex != int32(index) {
				t.Fatal("target order changed")
			}
			target := byID[down.TaskID]
			if len(target.Upstream) != 2 || len(target.Downstream) != 0 {
				t.Fatalf("target=%+v", target)
			}
			up := target.Upstream[down.PartitionIndex]
			if up.TaskID != source.TaskID || up.PartitionIndex != down.PartitionIndex {
				t.Fatalf("mismatched endpoints: %+v %+v", down, up)
			}
		}
	}
}
