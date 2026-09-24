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

func TestPhysicalWindowLateOutputGroups(t *testing.T) {
	graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{
		{OperatorID: "source", Type: rpc.OperatorTypeSource},
		{OperatorID: "window", Type: rpc.OperatorTypeWindow, LateOutputTag: "late"},
		{OperatorID: "main", Type: rpc.OperatorTypeSink},
		{OperatorID: "late", Type: rpc.OperatorTypeSink},
	}, Edges: []rpc.EdgeDescriptor{
		{SourceOperatorID: "source", TargetOperatorID: "window", Shuffle: rpc.ShuffleStrategyHash},
		{SourceOperatorID: "window", TargetOperatorID: "main", Shuffle: rpc.ShuffleStrategyForward},
		{SourceOperatorID: "window", TargetOperatorID: "late", SideOutput: "late", Shuffle: rpc.ShuffleStrategyHash},
	}}
	tasks, err := buildPhysicalTasks("job", graph, 2)
	if err != nil {
		t.Fatal(err)
	}
	windows := 0
	for _, task := range tasks {
		if task.OperatorID != "window" {
			continue
		}
		windows++
		if len(task.OutputGroups) != 2 || len(task.Downstream) != 3 {
			t.Fatalf("groups=%+v channels=%+v", task.OutputGroups, task.Downstream)
		}
		for _, group := range task.OutputGroups {
			want := "main"
			n := 1
			if group.SideOutput == "late" {
				want = "late"
				n = 2
			}
			if len(group.Streams) != n {
				t.Fatal("wrong partition count")
			}
			for _, index := range group.Streams {
				if task.Downstream[index].OperatorID != want {
					t.Fatal("tag routed to wrong operator")
				}
			}
		}
	}
	if windows != 2 {
		t.Fatal("window chain was fused across branches")
	}
	graph.Edges[2].SideOutput = "unknown"
	if _, err = buildPhysicalTasks("job", graph, 2); err == nil {
		t.Fatal("unknown side output accepted")
	}
}

func TestPhysicalTasksBroadcastAndProcessOutputs(t *testing.T) {
	graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{
		{OperatorID: "source", Type: rpc.OperatorTypeSource, Parallelism: 1},
		{OperatorID: "process", Type: rpc.OperatorTypeProcess, Parallelism: 2, SideOutputTags: []string{"audit"}},
		{OperatorID: "sink", Type: rpc.OperatorTypeSink, Parallelism: 2},
	}, Edges: []rpc.EdgeDescriptor{
		{SourceOperatorID: "source", TargetOperatorID: "process", Shuffle: rpc.ShuffleStrategyBroadcast},
		{SourceOperatorID: "process", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyForward, SideOutput: "audit"},
	}}
	if err := validateGraphWindows(graph); err != nil {
		t.Fatal(err)
	}
	tasks, err := buildPhysicalTasks("job", graph, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.OperatorID == "source" && (len(task.OutputGroups) != 1 || !task.OutputGroups[0].Broadcast || len(task.OutputGroups[0].Streams) != 2) {
			t.Fatalf("broadcast=%+v", task)
		}
		if task.OperatorID == "process" && (len(task.OutputGroups) != 1 || task.OutputGroups[0].SideOutput != "audit") {
			t.Fatalf("process=%+v", task)
		}
	}
	graph.Operators[1].SideOutputTags = []string{"audit", "audit"}
	if validateGraphWindows(graph) == nil {
		t.Fatal("duplicate output tags accepted")
	}
}
