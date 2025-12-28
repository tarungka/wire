package planner

import (
	"fmt"
)

// PhysicalPlan represents the distributed execution graph.
type PhysicalPlan struct {
	Tasks map[string]*Task
}

// Task is a single execution unit on a specific node.
type Task struct {
	ID          string
	OperatorID  string
	NodeID      string
	ParallelID  int
	InputTasks  []string
	OutputTasks []string
}

// Planner maps a LogicalPlan to a PhysicalPlan.
type Planner struct {
	clusterNodes []string
}

// NewPlanner creates a new planner with the given cluster nodes.
func NewPlanner(nodes []string) *Planner {
	return &Planner{clusterNodes: nodes}
}

// Plan converts a logical plan to a physical plan by distributing tasks across nodes.
func (p *Planner) Plan(logical *LogicalPlan) (*PhysicalPlan, error) {
	physical := &PhysicalPlan{
		Tasks: make(map[string]*Task),
	}

	// 1. Create physical tasks for each logical node
	for _, lNode := range logical.Nodes {
		for i := 0; i < lNode.Parallelism; i++ {
			nodeID := p.clusterNodes[(len(physical.Tasks))%len(p.clusterNodes)]
			taskID := fmt.Sprintf("%s-%d", lNode.ID, i)

			physical.Tasks[taskID] = &Task{
				ID:         taskID,
				OperatorID: lNode.ID,
				NodeID:     nodeID,
				ParallelID: i,
			}
		}
	}

	// 2. Connect physical tasks based on logical connections
	for _, lNode := range logical.Nodes {
		for _, outputID := range lNode.OutputIDs {
			targetLNode := logical.Nodes[outputID]

			// Simple all-to-all connection for now
			// In a real system, this would depend on the partitioning strategy (Keyed vs. Broadcast)
			for i := 0; i < lNode.Parallelism; i++ {
				sourceTaskID := fmt.Sprintf("%s-%d", lNode.ID, i)
				sourceTask := physical.Tasks[sourceTaskID]

				for j := 0; j < targetLNode.Parallelism; j++ {
					targetTaskID := fmt.Sprintf("%s-%d", targetLNode.ID, j)
					targetTask := physical.Tasks[targetTaskID]

					sourceTask.OutputTasks = append(sourceTask.OutputTasks, targetTaskID)
					targetTask.InputTasks = append(targetTask.InputTasks, sourceTaskID)
				}
			}
		}
	}

	return physical, nil
}
