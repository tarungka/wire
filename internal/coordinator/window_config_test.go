package coordinator

import (
	"errors"
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestWindowGraphValidationBeforeScheduling(t *testing.T) {
	valid := rpc.OperatorDescriptor{OperatorID: "window", Type: rpc.OperatorTypeWindow, Window: &rpc.WindowDefinition{Kind: "tumbling", Size: 10, AllowedLateness: 30}, LateOutputTag: "late"}
	for _, scenario := range []string{"valid", "negative", "wrong-type", "wrong-tag", "missing-source", "record-retry"} {
		t.Run(scenario, func(t *testing.T) {
			op := valid
			definition := *valid.Window
			op.Window = &definition
			edge := rpc.EdgeDescriptor{SourceOperatorID: "window", TargetOperatorID: "sink", SideOutput: "late"}
			switch scenario {
			case "record-retry":
				op.ErrorPolicy = &rpc.ErrorPolicy{OnExhausted: "drop"}
			case "negative":
				op.Window.AllowedLateness = -1
			case "wrong-type":
				op.Type = rpc.OperatorTypeMap
			case "wrong-tag":
				edge.SideOutput = "typo"
			case "missing-source":
				edge.SourceOperatorID = "absent"
			}
			graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{op, {OperatorID: "sink", Type: rpc.OperatorTypeSink}}, Edges: []rpc.EdgeDescriptor{edge}}
			_, err := validateGraphKeyGroups(graph, 1)
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected invalid config before scheduling, got %v", err)
			}
		})
	}
}
