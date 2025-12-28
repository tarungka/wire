package planner

import (
	"encoding/json"
	"fmt"

	"github.com/tarungka/wire/internal/analytics/operators"
)

// DSL represents the JSON structure of a query.
type DSL struct {
	Operators   []OperatorDef   `json:"operators"`
	Connections []ConnectionDef `json:"connections"`
}

type OperatorDef struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Parallelism int                    `json:"parallelism"`
	Config      map[string]interface{} `json:"config"`
}

type ConnectionDef struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ParseDSL parses a JSON DSL and returns a LogicalPlan.
func ParseDSL(data []byte) (*LogicalPlan, error) {
	var dsl DSL
	if err := json.Unmarshal(data, &dsl); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DSL: %w", err)
	}

	plan := NewLogicalPlan()

	// 1. Create operators
	for _, opDef := range dsl.Operators {
		op, err := operators.Create(opDef.Type, opDef.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to create operator %s: %w", opDef.ID, err)
		}

		parallelism := opDef.Parallelism
		if parallelism <= 0 {
			parallelism = 1
		}

		plan.AddNode(opDef.ID, op, parallelism)
	}

	// 2. Connect operators
	for _, connDef := range dsl.Connections {
		if _, ok := plan.Nodes[connDef.From]; !ok {
			return nil, fmt.Errorf("unknown source operator: %s", connDef.From)
		}
		if _, ok := plan.Nodes[connDef.To]; !ok {
			return nil, fmt.Errorf("unknown target operator: %s", connDef.To)
		}
		plan.Connect(connDef.From, connDef.To)
	}

	return plan, nil
}
