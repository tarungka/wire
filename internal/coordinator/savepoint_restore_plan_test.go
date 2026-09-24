package coordinator

import (
	"errors"
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func upgradePlanFixture(t *testing.T) (CheckpointMeta, []rpc.TaskDescriptor) {
	t.Helper()
	old, err := buildPhysicalTasks("old-job", linearGraph(), 2)
	if err != nil {
		t.Fatal(err)
	}
	next, err := buildPhysicalTasks("new-job", linearGraph(), 2)
	if err != nil {
		t.Fatal(err)
	}
	cp := CheckpointMeta{JobID: "old-job", ID: 7, SavepointID: "sp", Status: CheckpointCompleted, NumKeyGroups: 128, TaskDescriptors: old, Tasks: map[string]string{}, Replicas: map[string]string{}, StatePaths: map[string]string{}}
	for _, task := range old {
		cp.Tasks[task.TaskID] = "worker"
		cp.Replicas[task.TaskID] = "replica"
		cp.StatePaths[task.TaskID] = "replica"
	}
	return cp, next
}

func TestSavepointUpgradePlanPreservesArchiveIdentity(t *testing.T) {
	cp, tasks := upgradePlanFixture(t)
	// A code/config upgrade is intentional; positional state ownership is not.
	for i := range tasks {
		tasks[i].OperatorChain[1].ClassName = "new-code"
		tasks[i].OperatorChain[1].Config = []byte("new-config")
	}
	plan, err := planSavepointTaskRestore(cp, tasks)
	if err != nil {
		t.Fatal(err)
	}
	for i, task := range tasks {
		if plan[task.TaskID] != cp.TaskDescriptors[i].TaskID || plan[task.TaskID] == task.TaskID {
			t.Fatal("archive was rebound to target identity")
		}
	}
}

func TestSavepointUpgradePlanRejectsIncompatibleStateLayout(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "operator", "order", "type", "parallelism", "groups", "replica", "channel", "corrupt", "ownership", "saved-duplicate"} {
		t.Run(mode, func(t *testing.T) {
			cp, tasks := upgradePlanFixture(t)
			switch mode {
			case "missing":
				tasks = tasks[:1]
			case "duplicate":
				tasks[1] = tasks[0]
			case "operator":
				tasks[0].OperatorChain[1].OperatorID = "other"
			case "order":
				tasks[0].OperatorChain[0], tasks[0].OperatorChain[1] = tasks[0].OperatorChain[1], tasks[0].OperatorChain[0]
			case "type":
				tasks[0].OperatorChain[1].Type = rpc.OperatorTypeSink
			case "parallelism":
				tasks[0].Parallelism = 3
			case "groups":
				tasks[0].NumKeyGroups = 256
			case "replica":
				delete(cp.StatePaths, cp.TaskDescriptors[0].TaskID)
			case "channel":
				tasks[0].Downstream = append(tasks[0].Downstream, rpc.DownstreamChannelInfo{OperatorID: "other"})
			case "corrupt":
				cp.InvalidReason = "corrupt archive"
			case "ownership":
				tasks[0].KeyGroup.End--
				cp.TaskDescriptors[0].KeyGroup = tasks[0].KeyGroup
			case "saved-duplicate":
				cp.TaskDescriptors[1] = cp.TaskDescriptors[0]
			}
			if _, err := planSavepointTaskRestore(cp, tasks); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("accepted incompatible %s: %v", mode, err)
			}
		})
	}
}
