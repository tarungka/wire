package planner

import (
	"github.com/tarungka/wire/internal/analytics"
)

// LogicalPlan represents the user-defined DAG of operators.
type LogicalPlan struct {
	Nodes map[string]*LogicalNode
}

// LogicalNode represents a single operator in the logical plan.
type LogicalNode struct {
	ID          string
	Operator    analytics.Operator
	InputIDs    []string
	OutputIDs   []string
	Parallelism int
}

// NewLogicalPlan creates an empty logical plan.
func NewLogicalPlan() *LogicalPlan {
	return &LogicalPlan{
		Nodes: make(map[string]*LogicalNode),
	}
}

// AddNode adds an operator to the logical plan.
func (p *LogicalPlan) AddNode(id string, op analytics.Operator, parallelism int) *LogicalNode {
	node := &LogicalNode{
		ID:          id,
		Operator:    op,
		Parallelism: parallelism,
	}
	p.Nodes[id] = node
	return node
}

// Connect links two nodes in the DAG.
func (p *LogicalPlan) Connect(fromID, toID string) {
	if fromNode, ok := p.Nodes[fromID]; ok {
		if toNode, ok := p.Nodes[toID]; ok {
			fromNode.OutputIDs = append(fromNode.OutputIDs, toID)
			toNode.InputIDs = append(toNode.InputIDs, fromID)
		}
	}
}
