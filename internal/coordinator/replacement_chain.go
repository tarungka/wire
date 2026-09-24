package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/rpc"
)

// replacementChainIndexes retains stateful operators in order and permits
// stateless map/filter/flat-map insertion or removal. Removed state is checked
// again against the actual snapshot on the worker. Source state stays separate;
// returned indexes address TaskCheckpoint.Operators, with -1 for new operators.
func replacementChainIndexes(source, target []rpc.OperatorDescriptor) ([]int, error) {
	invalid := func() ([]int, error) {
		return nil, fmt.Errorf("%w: replacement must preserve stateful operators and may only edit stateless transforms", ErrInvalidConfig)
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
	newIDs := make(map[string]bool, len(target))
	for _, op := range target {
		if op.OperatorID == "" || newIDs[op.OperatorID] {
			return invalid()
		}
		newIDs[op.OperatorID] = true
	}
	stateless := func(op rpc.OperatorDescriptor) bool {
		return op.Type == rpc.OperatorTypeMap || op.Type == rpc.OperatorTypeFilter || op.Type == rpc.OperatorTypeFlatMap
	}
	changed := false
	seen := make(map[string]bool, len(target))
	var indexes []int
	oldPosition, oldSnapshot := 0, 0
	for _, next := range target {
		if next.OperatorID == "" || seen[next.OperatorID] {
			return invalid()
		}
		seen[next.OperatorID] = true
		for oldPosition < len(source) && !newIDs[source[oldPosition].OperatorID] {
			if !stateless(source[oldPosition]) {
				return invalid()
			}
			oldPosition++
			oldSnapshot++
			changed = true
		}
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
			changed = true
		default:
			return invalid()
		}
	}
	for oldPosition < len(source) {
		if newIDs[source[oldPosition].OperatorID] || !stateless(source[oldPosition]) {
			return invalid()
		}
		oldPosition++
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return indexes, nil
}
