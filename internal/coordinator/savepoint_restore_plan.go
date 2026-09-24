package coordinator

import (
	"fmt"
	"reflect"

	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/rpc"
)

// planSavepointTaskRestore maps an unchanged physical state layout into a new
// job namespace. It deliberately retains source task IDs: archive identity must
// not be rewritten to make it look like the target created the checkpoint.
// Operator code/config may change, but the application must retain compatible
// state serializers. Redistribution uses the separate rescale planning path.
func planSavepointTaskRestore(cp CheckpointMeta, targets []rpc.TaskDescriptor) (map[string]string, error) {
	invalid := func(reason string) (map[string]string, error) {
		return nil, fmt.Errorf("%w: savepoint incompatible with job graph: %s", ErrInvalidConfig, reason)
	}
	if cp.Status != CheckpointCompleted || cp.SavepointID == "" || cp.InvalidReason != "" {
		return invalid("savepoint is not a valid completed boundary")
	}
	if err := (keygroup.Config{NumKeyGroups: cp.NumKeyGroups, Parallelism: 1}).Validate(); err != nil {
		return invalid("invalid key-group count")
	}
	if len(cp.TaskDescriptors) == 0 || len(cp.TaskDescriptors) != len(cp.Tasks) || len(targets) != len(cp.Tasks) {
		return invalid("task inventory differs")
	}
	for _, source := range cp.TaskDescriptors {
		if cp.Tasks[source.TaskID] == "" || cp.Replicas[source.TaskID] == "" || cp.StatePaths[source.TaskID] != cp.Replicas[source.TaskID] {
			return invalid("source has no durable replica")
		}
	}
	return planTaskLayoutRestore(cp.NumKeyGroups, cp.TaskDescriptors, targets)
}

// planTaskLayoutRestore checks structural restore compatibility without claiming
// that a savepoint exists or that application state serializers are compatible.
func planTaskLayoutRestore(numKeyGroups int, descriptors, targets []rpc.TaskDescriptor) (map[string]string, error) {
	return planTaskLayoutRestoreMode(numKeyGroups, descriptors, targets, false)
}

func planTaskLayoutRestoreMode(numKeyGroups int, descriptors, targets []rpc.TaskDescriptor, insertions bool) (map[string]string, error) {
	invalid := func(reason string) (map[string]string, error) {
		return nil, fmt.Errorf("%w: savepoint incompatible with job graph: %s", ErrInvalidConfig, reason)
	}
	if len(descriptors) == 0 || len(descriptors) != len(targets) {
		return invalid("task inventory differs")
	}
	type identity struct {
		operator string
		index    int32
	}
	sources := make(map[identity]rpc.TaskDescriptor)
	sourceIDs := make(map[string]bool)
	counts := make(map[string]int)
	for _, source := range descriptors {
		id := identity{source.OperatorID, source.SubtaskIndex}
		if source.TaskID == "" || sourceIDs[source.TaskID] || source.OperatorID == "" {
			return invalid("invalid source identity")
		}
		if _, duplicate := sources[id]; duplicate {
			return invalid("duplicate source operator instance")
		}
		sources[id] = source
		sourceIDs[source.TaskID] = true
		counts[source.OperatorID]++
	}
	for _, source := range sources {
		if counts[source.OperatorID] != int(source.Parallelism) {
			return invalid("incomplete source operator instances")
		}
	}
	result := make(map[string]string)
	consumed := make(map[identity]bool)
	for _, target := range targets {
		id := identity{target.OperatorID, target.SubtaskIndex}
		source, exists := sources[id]
		if !exists || consumed[id] || target.TaskID == "" || result[target.TaskID] != "" {
			return invalid("target operator identities differ")
		}
		if source.NumKeyGroups != numKeyGroups || target.NumKeyGroups != numKeyGroups || source.Parallelism < 1 || source.SubtaskIndex < 0 || source.SubtaskIndex >= source.Parallelism || source.Parallelism != target.Parallelism || source.KeyGroup != target.KeyGroup {
			return invalid("state ownership changed; redistribute state before upgrading")
		}
		groups, err := keygroup.AllTaskRanges(numKeyGroups, int(source.Parallelism))
		if err != nil || source.KeyGroup.Start != int32(groups[source.SubtaskIndex].Start) || source.KeyGroup.End != int32(groups[source.SubtaskIndex].End)-1 {
			return invalid("invalid saved ownership")
		}
		if insertions {
			if _, err := replacementChainIndexes(source.OperatorChain, target.OperatorChain); err != nil {
				return nil, err
			}
		} else {
			if len(source.OperatorChain) == 0 || len(source.OperatorChain) != len(target.OperatorChain) {
				return invalid("operator chain layout differs")
			}
			for i, old := range source.OperatorChain {
				next := target.OperatorChain[i]
				if (old.Type == rpc.OperatorTypeProcess || old.Type == rpc.OperatorTypeWindow) && stateBackendKind(old.StateBackend) != stateBackendKind(next.StateBackend) {
					return invalid(fmt.Sprintf("state backend mismatch: checkpoint uses %q, job configured with %q", stateBackendKind(old.StateBackend), stateBackendKind(next.StateBackend)))
				}
				if old.OperatorID == "" || old.OperatorID != next.OperatorID || old.Type != next.Type {
					return invalid("operator chain identity or order differs")
				}
			}
		}
		if !sameSavepointRoutes(source, target) {
			return invalid("channel topology differs")
		}
		consumed[id] = true
		result[target.TaskID] = source.TaskID
	}
	return result, nil
}

func sameSavepointRoutes(source, target rpc.TaskDescriptor) bool {
	// Task IDs, worker ownership and addresses change across deployments, while operator
	// identities and channel indexes determine saved barrier/input semantics.
	normalize := func(task rpc.TaskDescriptor) rpc.TaskDescriptor {
		result := rpc.TaskDescriptor{OutputKeyGroups: task.OutputKeyGroups, OutputGroups: task.OutputGroups}
		for _, input := range task.Upstream {
			input.TaskID, input.WorkerID, input.Address, input.IdleTimeout = "", "", "", 0
			result.Upstream = append(result.Upstream, input)
		}
		for _, output := range task.Downstream {
			output.TaskID, output.Address = "", ""
			result.Downstream = append(result.Downstream, output)
		}
		return result
	}
	return reflect.DeepEqual(normalize(source), normalize(target))
}

func stateBackendKind(spec *rpc.StateBackendSpec) string {
	if spec == nil || spec.Type == "" {
		return "pebble"
	}
	return spec.Type
}
