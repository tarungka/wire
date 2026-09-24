package engine

import (
	"context"
	"fmt"

	"github.com/tarungka/wire/internal/keygroup"
)

// KeyGroupStateRestorer restores typed keyed snapshots before task processing.
// Implementations can delegate to either backend's RestoreKeyGroupRanges method.
type KeyGroupStateRestorer interface {
	RestoreKeyGroupState(context.Context, keygroup.KeyGroupRange, []KeyGroupSnapshot) error
}

// OperatorRescaleState describes one operator's assigned state after rescaling.
// OperatorIndex -1 addresses the source; other indexes address task operators.
type OperatorRescaleState struct {
	OperatorIndex int
	Assigned      keygroup.KeyGroupRange
	Parts         []KeyGroupSnapshot
}

func (ts *TaskSlot) restoreRescaledOperators(ctx context.Context) error {
	targets := make([]KeyGroupStateRestorer, len(ts.RescaleState))
	seen := make(map[int]bool)
	for i, state := range ts.RescaleState {
		if seen[state.OperatorIndex] {
			return fmt.Errorf("duplicate rescaled operator %d", state.OperatorIndex)
		}
		seen[state.OperatorIndex] = true
		var op Operator
		if state.OperatorIndex == -1 {
			op = ts.Source
		} else if state.OperatorIndex >= 0 && state.OperatorIndex < len(ts.Operators) {
			op = ts.Operators[state.OperatorIndex]
		}
		target, ok := op.(KeyGroupStateRestorer)
		if !ok {
			return fmt.Errorf("operator %d cannot restore key-group state", state.OperatorIndex)
		}
		targets[i] = target
	}
	for i, state := range ts.RescaleState {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := invokeOperator(func() error { return targets[i].RestoreKeyGroupState(ctx, state.Assigned, state.Parts) }); err != nil {
			return fmt.Errorf("rescale operator %d: %w", state.OperatorIndex, err)
		}
	}
	return nil
}
