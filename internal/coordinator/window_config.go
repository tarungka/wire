package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/rpc"
)

// Validate at submission as well as deployment, before a job can be stopped or scheduled.
func validateGraphWindows(graph rpc.JobGraph) error {
	operators := make(map[string]rpc.OperatorDescriptor)
	for _, op := range graph.Operators {
		operators[op.OperatorID] = op
		if err := op.ValidateSideOutputs(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if op.Type == rpc.OperatorTypeWindow && op.ErrorPolicy != nil {
			return fmt.Errorf("%w: window errors require task recovery, not record error policies", ErrInvalidConfig)
		}
		if op.Window != nil {
			if op.Type != rpc.OperatorTypeWindow {
				return fmt.Errorf("%w: window definition requires window operator", ErrInvalidConfig)
			}
			if err := op.Window.Validate(); err != nil {
				return fmt.Errorf("%w: operator %q: %v", ErrInvalidConfig, op.OperatorID, err)
			}
		}
		if op.LateOutputTag != "" && op.Type != rpc.OperatorTypeWindow {
			return fmt.Errorf("%w: late output requires window operator", ErrInvalidConfig)
		}
	}
	for _, edge := range graph.Edges {
		if edge.SideOutput == "" {
			continue
		}
		op, ok := operators[edge.SourceOperatorID]
		if !ok || !op.HasSideOutput(edge.SideOutput) {
			return fmt.Errorf("%w: unknown side output %q on %q", ErrInvalidConfig, edge.SideOutput, edge.SourceOperatorID)
		}
	}
	return nil
}
