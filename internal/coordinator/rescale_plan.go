package coordinator

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/rpc"
)

// RescaleStatePart identifies the old task holding a portion of a new task's
// state. Groups use the inclusive RPC interval convention.
type RescaleStatePart = rpc.RescaleStatePart

// planRescaleState preserves old-owner boundaries while covering each new
// assignment exactly. Operator identity must survive a parallelism change.
func planRescaleState(checkpoint CheckpointMeta, oldTasks, newTasks []rpc.TaskDescriptor) (map[string][]RescaleStatePart, error) {
	if checkpoint.Status != CheckpointCompleted || checkpoint.NumKeyGroups < 1 {
		return nil, fmt.Errorf("rescale requires a completed checkpoint with key-group metadata")
	}
	if err := (keygroup.Config{NumKeyGroups: checkpoint.NumKeyGroups, Parallelism: 1}).Validate(); err != nil {
		return nil, err
	}
	if len(oldTasks) == 0 || len(oldTasks) != len(checkpoint.Tasks) {
		return nil, fmt.Errorf("savepoint topology does not cover all source tasks")
	}
	seenSources := make(map[string]bool)
	oldByOperator := make(map[string][]rpc.TaskDescriptor)
	for _, task := range oldTasks {
		if task.TaskID == "" || seenSources[task.TaskID] {
			return nil, fmt.Errorf("duplicate or empty source task identity")
		}
		seenSources[task.TaskID] = true
		if task.NumKeyGroups != checkpoint.NumKeyGroups || task.KeyGroup.Start < 0 || task.KeyGroup.End < task.KeyGroup.Start || int(task.KeyGroup.End) >= checkpoint.NumKeyGroups {
			return nil, fmt.Errorf("invalid source assignment %s", task.TaskID)
		}
		if checkpoint.Tasks[task.TaskID] == "" || checkpoint.Replicas[task.TaskID] == "" || checkpoint.StatePaths[task.TaskID] != checkpoint.Replicas[task.TaskID] {
			return nil, fmt.Errorf("source task %s has no durable replica", task.TaskID)
		}
		oldByOperator[task.OperatorID] = append(oldByOperator[task.OperatorID], task)
	}
	for operator, tasks := range oldByOperator {
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].KeyGroup.Start < tasks[j].KeyGroup.Start })
		next := int32(0)
		for _, task := range tasks {
			if !sameRescaleChain(tasks[0].OperatorChain, task.OperatorChain) {
				return nil, fmt.Errorf("inconsistent source operator chain %s", operator)
			}
			if task.KeyGroup.Start != next {
				return nil, fmt.Errorf("source operator %s has gaps or overlaps", operator)
			}
			next = task.KeyGroup.End + 1
		}
		if int(next) != checkpoint.NumKeyGroups {
			return nil, fmt.Errorf("source operator %s has incomplete ownership", operator)
		}
	}
	targetByOperator := make(map[string][]rpc.TaskDescriptor)
	for _, task := range newTasks {
		targetByOperator[task.OperatorID] = append(targetByOperator[task.OperatorID], task)
	}
	if len(targetByOperator) != len(oldByOperator) {
		return nil, fmt.Errorf("rescale cannot add or remove operator chains")
	}
	for operator, targets := range targetByOperator {
		sources := oldByOperator[operator]
		if len(sources) == 0 {
			return nil, fmt.Errorf("unknown rescale operator %s", operator)
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].KeyGroup.Start < targets[j].KeyGroup.Start })
		next := int32(0)
		for _, target := range targets {
			if target.KeyGroup.Start != next {
				return nil, fmt.Errorf("target operator %s has gaps or overlaps", operator)
			}
			next = target.KeyGroup.End + 1
			if !sameRescaleChain(sources[0].OperatorChain, target.OperatorChain) {
				return nil, fmt.Errorf("rescale changes operator chain %s", operator)
			}
		}
		if int(next) != checkpoint.NumKeyGroups {
			return nil, fmt.Errorf("target operator %s has incomplete ownership", operator)
		}
	}
	result := make(map[string][]RescaleStatePart)
	for _, target := range newTasks {
		if target.NumKeyGroups != checkpoint.NumKeyGroups {
			return nil, fmt.Errorf("key group count mismatch (savepoint: %d, job: %d)", checkpoint.NumKeyGroups, target.NumKeyGroups)
		}
		if _, exists := result[target.TaskID]; exists {
			return nil, fmt.Errorf("duplicate target task %s", target.TaskID)
		}
		if target.KeyGroup.Start < 0 || target.KeyGroup.End < target.KeyGroup.Start || int(target.KeyGroup.End) >= checkpoint.NumKeyGroups {
			return nil, fmt.Errorf("invalid target assignment %s", target.TaskID)
		}
		next := target.KeyGroup.Start
		for _, source := range oldByOperator[target.OperatorID] {
			start, end := max(source.KeyGroup.Start, target.KeyGroup.Start), min(source.KeyGroup.End, target.KeyGroup.End)
			if start > end {
				continue
			}
			if start != next {
				return nil, fmt.Errorf("incomplete source coverage for %s", target.TaskID)
			}
			result[target.TaskID] = append(result[target.TaskID], RescaleStatePart{SourceTaskID: source.TaskID, ReplicaAddress: checkpoint.Replicas[source.TaskID], Groups: rpc.KeyGroupRange{Start: start, End: end}})
			next = end + 1
		}
		if next != target.KeyGroup.End+1 {
			return nil, fmt.Errorf("missing source operator for %s", target.TaskID)
		}
	}
	return result, nil
}

// Per-operator parallelism may change; code, configuration and identity may not.
func sameRescaleChain(left, right []rpc.OperatorDescriptor) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		a, b := left[i], right[i]
		a.Parallelism, b.Parallelism = 0, 0
		if !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}
