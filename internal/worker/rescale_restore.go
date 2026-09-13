package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/rpc"
)

// fetchRescaleState retains source snapshot identities while assembling the
// disjoint ranges assigned to this deployment. No operator runs during fetch.
func (w *Worker) fetchRescaleState(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor) ([]engine.OperatorRescaleState, error) {
	return assembleRescaleState(ctx, jobID, taskID, desc, w.fetchTaskCheckpoint)
}

func assembleRescaleState(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor, fetch func(context.Context, string, string, rpc.TaskDescriptor) (*engine.TaskCheckpoint, error)) ([]engine.OperatorRescaleState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	restore := desc.RestoreRescale
	if restore == nil || desc.RestoreCheckpoint != nil || restore.CheckpointID == 0 || restore.EpochID == 0 || restore.NumKeyGroups != desc.NumKeyGroups {
		return nil, fmt.Errorf("invalid rescale checkpoint identity")
	}
	if err := (keygroup.Config{NumKeyGroups: desc.NumKeyGroups, Parallelism: int(desc.Parallelism)}).Validate(); err != nil {
		return nil, err
	}
	if desc.KeyGroup.Start < 0 || desc.KeyGroup.End < desc.KeyGroup.Start || int(desc.KeyGroup.End) >= desc.NumKeyGroups {
		return nil, fmt.Errorf("invalid rescale assignment")
	}
	parts := append([]rpc.RescaleStatePart(nil), restore.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].Groups.Start < parts[j].Groups.Start })
	next := desc.KeyGroup.Start
	for _, part := range parts {
		if part.SourceTaskID == "" || part.ReplicaAddress == "" || part.Groups.Start != next || part.Groups.End < part.Groups.Start || part.Groups.End > desc.KeyGroup.End {
			return nil, fmt.Errorf("invalid rescale state coverage")
		}
		next = part.Groups.End + 1
	}
	if next != desc.KeyGroup.End+1 {
		return nil, fmt.Errorf("incomplete rescale state coverage")
	}
	hasSource, operators := false, 0
	for _, op := range desc.OperatorChain {
		if op.Type == rpc.OperatorTypeSource {
			hasSource = true
		} else {
			operators++
		}
	}
	assigned := keygroup.KeyGroupRange{Start: uint16(desc.KeyGroup.Start), End: uint16(desc.KeyGroup.End + 1)}
	states := make(map[int]*engine.OperatorRescaleState)
	typedIndexes := make(map[int]bool)
	for partIndex, part := range parts {
		fetchDesc := desc
		fetchDesc.RestoreCheckpoint = &rpc.CheckpointRestoreDescriptor{SourceTaskID: part.SourceTaskID, ReplicaAddress: part.ReplicaAddress, CheckpointID: restore.CheckpointID, EpochID: restore.EpochID}
		snapshot, err := fetch(ctx, jobID, taskID, fetchDesc)
		if err != nil {
			return nil, err
		}
		if snapshot.TaskID != part.SourceTaskID || snapshot.CheckpointID != restore.CheckpointID || snapshot.EpochID != restore.EpochID || snapshot.HasSource != hasSource || len(snapshot.Operators) != operators {
			return nil, fmt.Errorf("rescale snapshot topology or identity mismatch")
		}
		if err := snapshot.ValidateStateHandles(); err != nil {
			return nil, err
		}
		typed := make(map[int]bool)
		for _, index := range snapshot.StateHandleIndexes {
			typed[index] = true
		}
		start := 0
		if hasSource {
			start = -1
		}
		for index := start; index < operators; index++ {
			data := snapshot.Source
			if index >= 0 {
				data = snapshot.Operators[index]
			}
			if partIndex == 0 {
				typedIndexes[index] = typed[index]
			} else if typedIndexes[index] != typed[index] {
				return nil, fmt.Errorf("inconsistent rescale state type for operator %d", index)
			}
			if !typed[index] {
				if len(data) != 0 {
					return nil, fmt.Errorf("operator %d has opaque state requiring an explicit redistribution strategy", index)
				}
				continue
			}
			var handle engine.SnapshotHandle
			if err := json.Unmarshal(data, &handle); err != nil {
				return nil, err
			}
			if states[index] == nil {
				states[index] = &engine.OperatorRescaleState{OperatorIndex: index, Assigned: assigned}
			}
			states[index].Parts = append(states[index].Parts, engine.KeyGroupSnapshot{Groups: keygroup.KeyGroupRange{Start: uint16(part.Groups.Start), End: uint16(part.Groups.End + 1)}, Snapshot: handle})
		}
	}
	result := make([]engine.OperatorRescaleState, 0, len(states))
	for _, state := range states {
		result = append(result, *state)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].OperatorIndex < result[j].OperatorIndex })
	return result, nil
}
