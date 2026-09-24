package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// Resolve on submission, never deployment: changing a coordinator default must
// not change a persisted job's backend on restart or on worker replacement.
func (c *Coordinator) resolveStateBackendDefaults(raw []byte) ([]byte, error) {
	var graph rpc.JobGraph
	if protocol.DecodeMsgPack(raw, &graph) != nil {
		return raw, nil
	} // Legacy opaque config.
	changed := false
	for i := range graph.Operators {
		op := &graph.Operators[i]
		if op.StateBackend == nil && c.config.DefaultStateBackend != nil && (op.Type == rpc.OperatorTypeProcess || op.Type == rpc.OperatorTypeWindow) {
			spec := *c.config.DefaultStateBackend
			if spec.Type == "hashmap" {
				spec.DataDir = ""
			} else {
				spec.MaxMemoryBytes = 0
			}
			op.StateBackend = &spec
			changed = true
		}
		if err := op.ValidateStateBackend(); err != nil {
			return nil, fmt.Errorf("%w: operator %q: %v", ErrInvalidConfig, op.OperatorID, err)
		}
	}
	if !changed {
		return raw, nil
	}
	encoded, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		return nil, fmt.Errorf("%w: encoding state backend defaults: %v", ErrInvalidConfig, err)
	}
	return encoded, nil
}
