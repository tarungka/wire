package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/rpc"
)

func validateGraphWatermarks(graph rpc.JobGraph) error {
	for _, op := range graph.Operators {
		if op.Watermark == nil {
			continue
		}
		if op.Type != rpc.OperatorTypeSource {
			return fmt.Errorf("%w: watermark strategy requires a source", ErrInvalidConfig)
		}
		if err := op.Watermark.Validate(); err != nil {
			return fmt.Errorf("%w: operator %q: %v", ErrInvalidConfig, op.OperatorID, err)
		}
	}
	return nil
}
