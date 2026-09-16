package coordinator

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCheckpointManifestPhysicalTopology(t *testing.T) {
	graph := rpc.JobGraph{NumKeyGroups: 4, Operators: []rpc.OperatorDescriptor{{OperatorID: "source", Type: rpc.OperatorTypeSource, Parallelism: 2}, {OperatorID: "map", Type: rpc.OperatorTypeMap, Parallelism: 2}}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "map", Shuffle: rpc.ShuffleStrategyForward}}}
	tasks, err := buildPhysicalTasks("job", graph, 2)
	if err != nil {
		t.Fatal(err)
	}
	cp := CheckpointMeta{ID: 1, JobID: "job", NumKeyGroups: 4, Timestamp: time.Now(), TaskDescriptors: tasks, Tasks: map[string]string{}}
	inventory := map[string]engine.TaskMeta{}
	for i, task := range tasks {
		cp.Tasks[task.TaskID] = "worker"
		inventory[task.TaskID] = engine.TaskMeta{TaskID: task.TaskID, StatePath: []string{"task-0", "task-1"}[i], StateFiles: []string{"checkpoint.archive"}, StateSizeBytes: 10}
	}
	manifest, err := checkpointManifest(&JobMeta{ID: "job", Name: "example"}, cp, inventory, cp.Timestamp.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Tasks) != 2 || manifest.DurationMs != 1000 {
		t.Fatalf("bad manifest: %+v", manifest)
	}
	// Persist the half-open schema range while RPC descriptors remain inclusive.
	if manifest.Tasks[0].KeyGroupRange.End != 2 {
		t.Fatal("key-group end conversion failed")
	}
	raw, err := engine.MarshalCheckpointMetadata(manifest)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := engine.UnmarshalCheckpointMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.ValidateComplete(); err != nil {
		t.Fatal(err)
	}
	delete(inventory, tasks[0].TaskID)
	if _, err := checkpointManifest(&JobMeta{ID: "job"}, cp, inventory, time.Now()); err == nil {
		t.Fatal("incomplete ACK inventory accepted")
	}
}
