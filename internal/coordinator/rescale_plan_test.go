package coordinator

import (
	"fmt"
	"testing"
)

func TestRescalePlanPreservesOldOwners(t *testing.T) {
	for _, sizes := range [][2]int{{4, 8}, {8, 4}, {4, 3}} {
		t.Run(fmt.Sprint(sizes), func(t *testing.T) {
			graph := linearGraph()
			old, err := buildPhysicalTasks("job", graph, sizes[0])
			if err != nil {
				t.Fatal(err)
			}
			next, err := buildPhysicalTasks("job", graph, sizes[1])
			if err != nil {
				t.Fatal(err)
			}
			cp := CheckpointMeta{Status: CheckpointCompleted, NumKeyGroups: 128, Tasks: map[string]string{}, Replicas: map[string]string{}, StatePaths: map[string]string{}}
			for _, task := range old {
				cp.Tasks[task.TaskID] = "worker"
				cp.Replicas[task.TaskID] = "replica/" + task.TaskID
				cp.StatePaths[task.TaskID] = cp.Replicas[task.TaskID]
			}
			plan, err := planRescaleState(cp, old, next)
			if err != nil {
				t.Fatal(err)
			}
			for group := int32(0); group < 128; group++ {
				matches := 0
				for _, parts := range plan {
					for _, part := range parts {
						if group >= part.Groups.Start && group <= part.Groups.End {
							matches++
							found := false
							for _, task := range old {
								if task.TaskID == part.SourceTaskID && group >= task.KeyGroup.Start && group <= task.KeyGroup.End {
									found = true
								}
							}
							if !found {
								t.Fatalf("group %d wrong source %s", group, part.SourceTaskID)
							}
						}
					}
				}
				if matches != 1 {
					t.Fatalf("group %d assigned %d times", group, matches)
				}
			}
		})
	}
}

func TestRescalePlanRejectsIncompleteOrChangedTargets(t *testing.T) {
	graph := linearGraph()
	old, err := buildPhysicalTasks("job", graph, 4)
	if err != nil {
		t.Fatal(err)
	}
	cp := CheckpointMeta{Status: CheckpointCompleted, NumKeyGroups: 128, Tasks: map[string]string{}, Replicas: map[string]string{}, StatePaths: map[string]string{}}
	for _, task := range old {
		cp.Tasks[task.TaskID] = "worker"
		cp.Replicas[task.TaskID] = "replica"
		cp.StatePaths[task.TaskID] = "replica"
	}
	for _, mode := range []string{"missing", "overlap", "changed-code", "changed-config", "changed-count"} {
		t.Run(mode, func(t *testing.T) {
			targets, err := buildPhysicalTasks("job", linearGraph(), 3)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
				targets = targets[:2]
			case "overlap":
				targets[1].KeyGroup.Start--
			case "changed-code":
				targets[0].OperatorChain[0].ClassName = "different"
			case "changed-config":
				targets[0].OperatorChain[0].Config = []byte("different")
			case "changed-count":
				targets[0].NumKeyGroups = 256
			}
			if _, err := planRescaleState(cp, old, targets); err == nil {
				t.Fatal("accepted invalid target plan")
			}
		})
	}
}

func TestRescalePlanRejectsCorruptSourceTopology(t *testing.T) {
	for _, mode := range []string{"duplicate", "missing", "mixed-chain", "invalid-count"} {
		t.Run(mode, func(t *testing.T) {
			old, err := buildPhysicalTasks("job", linearGraph(), 4)
			if err != nil {
				t.Fatal(err)
			}
			targets, err := buildPhysicalTasks("job", linearGraph(), 3)
			if err != nil {
				t.Fatal(err)
			}
			cp := CheckpointMeta{Status: CheckpointCompleted, NumKeyGroups: 128, Tasks: map[string]string{}, Replicas: map[string]string{}, StatePaths: map[string]string{}}
			for _, task := range old {
				cp.Tasks[task.TaskID] = "worker"
				cp.Replicas[task.TaskID] = "replica"
				cp.StatePaths[task.TaskID] = "replica"
			}
			switch mode {
			case "duplicate":
				old[1].TaskID = old[0].TaskID
			case "missing":
				old = old[:3]
			case "mixed-chain":
				old[1].OperatorChain = nil
			case "invalid-count":
				cp.NumKeyGroups = 127
			}
			if _, err := planRescaleState(cp, old, targets); err == nil {
				t.Fatal("corrupt topology accepted")
			}
		})
	}
}
