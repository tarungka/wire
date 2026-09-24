package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/rpc"
)

// replacementChainIndexes retains every existing operator in order and permits
// only new stateless map/filter/flat-map operators. Source state stays separate;
// returned indexes address TaskCheckpoint.Operators, with -1 for new operators.
func replacementChainIndexes(source, target []rpc.OperatorDescriptor) ([]int, error) {
	invalid := func() ([]int, error) {
		return nil, fmt.Errorf("%w: replacement must preserve saved operators and may only insert stateless transforms", ErrInvalidConfig)
	}
	if len(source) == 0 {
		return invalid()
	}
	oldIDs := make(map[string]bool, len(source))
	for _, op := range source {
		if op.OperatorID == "" || oldIDs[op.OperatorID] {
			return invalid()
		}
		oldIDs[op.OperatorID] = true
	}
	seen := make(map[string]bool, len(target))
	var indexes []int
	oldPosition, oldSnapshot := 0, 0
	for _, next := range target {
		if next.OperatorID == "" || seen[next.OperatorID] {
			return invalid()
		}
		seen[next.OperatorID] = true
		if oldPosition < len(source) && source[oldPosition].OperatorID == next.OperatorID {
			old := source[oldPosition]
			if old.Type != next.Type {
				return invalid()
			}
			if (old.Type == rpc.OperatorTypeProcess || old.Type == rpc.OperatorTypeWindow) && stateBackendKind(old.StateBackend) != stateBackendKind(next.StateBackend) {
				return invalid()
			}
			oldPosition++
			if old.Type != rpc.OperatorTypeSource {
				indexes = append(indexes, oldSnapshot)
				oldSnapshot++
			}
			continue
		}
		if oldIDs[next.OperatorID] {
			return invalid()
		}
		switch next.Type {
		case rpc.OperatorTypeMap, rpc.OperatorTypeFilter, rpc.OperatorTypeFlatMap:
			indexes = append(indexes, -1)
		default:
			return invalid()
		}
	}
	if oldPosition != len(source) {
		return invalid()
	}
	if len(source) == len(target) {
		return nil, nil
	}
	return indexes, nil
}
