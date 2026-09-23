package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/rpc"
)

func validateGraphKeyGroups(graph rpc.JobGraph, parallelism int) (int, error) {
	if err := validateGraphWatermarks(graph); err != nil {
		return 0, err
	}
	if err := validateGraphWindows(graph); err != nil {
		return 0, err
	}
	count := graph.NumKeyGroups
	if count == 0 {
		count = keygroup.DefaultNumKeyGroups
	}
	if err := (keygroup.Config{NumKeyGroups: count, Parallelism: parallelism}).Validate(); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	for _, op := range graph.Operators {
		if op.Parallelism != 0 {
			if err := (keygroup.Config{NumKeyGroups: count, Parallelism: int(op.Parallelism)}).Validate(); err != nil {
				return 0, fmt.Errorf("%w: operator %q: %v", ErrInvalidConfig, op.OperatorID, err)
			}
		}
	}
	return count, nil
}
