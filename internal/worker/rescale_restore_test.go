package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestAssembleRescaleState(t *testing.T) {
	for _, scenario := range []string{"merge", "gap", "overlap", "wrong-epoch", "opaque", "mixed-type", "wrong-topology", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			desc := rpc.TaskDescriptor{NumKeyGroups: 128, Parallelism: 3, KeyGroup: rpc.KeyGroupRange{Start: 42, End: 84}, OperatorChain: []rpc.OperatorDescriptor{{Type: rpc.OperatorTypeMap}}, RestoreRescale: &rpc.RescaleRestoreDescriptor{CheckpointID: 7, EpochID: 2, NumKeyGroups: 128, Parts: []rpc.RescaleStatePart{{SourceTaskID: "old-b", ReplicaAddress: "replica-b", Groups: rpc.KeyGroupRange{Start: 64, End: 84}}, {SourceTaskID: "old-a", ReplicaAddress: "replica-a", Groups: rpc.KeyGroupRange{Start: 42, End: 63}}}}}
			if scenario == "gap" {
				desc.RestoreRescale.Parts[0].Groups.Start++
			}
			if scenario == "overlap" {
				desc.RestoreRescale.Parts[0].Groups.Start--
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			calls := 0
			states, err := assembleRescaleState(ctx, "job", "new", desc, func(_ context.Context, job, target string, request rpc.TaskDescriptor) (*engine.TaskCheckpoint, error) {
				calls++
				if job != "job" || target != "new" || request.RestoreCheckpoint.SourceTaskID == "" {
					t.Fatal("lost source or target identity")
				}
				handle, _ := json.Marshal(engine.SnapshotHandle{BackendType: engine.StateBackendPebble, CheckpointID: 7, Data: []byte(request.RestoreCheckpoint.SourceTaskID)})
				snapshot := &engine.TaskCheckpoint{TaskID: request.RestoreCheckpoint.SourceTaskID, CheckpointID: 7, EpochID: 2, Operators: [][]byte{handle}, StateHandleIndexes: []int{0}}
				if scenario == "wrong-epoch" {
					snapshot.EpochID++
				}
				if scenario == "opaque" {
					snapshot.StateHandleIndexes = nil
				}
				if scenario == "mixed-type" && calls == 2 {
					snapshot.StateHandleIndexes = nil
					snapshot.Operators[0] = nil
				}
				if scenario == "wrong-topology" {
					snapshot.Operators = nil
				}
				return snapshot, nil
			})
			if scenario != "merge" {
				if err == nil || states != nil {
					t.Fatalf("invalid restore accepted: %v", states)
				}
				if (scenario == "gap" || scenario == "overlap" || scenario == "canceled") && calls != 0 {
					t.Fatal("invalid assignment fetched state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || len(states) != 1 || states[0].Assigned.Start != 42 || states[0].Assigned.End != 85 || len(states[0].Parts) != 2 {
				t.Fatalf("incorrect assembly: %+v", states)
			}
			parts := states[0].Parts
			if parts[0].Groups.Start != 42 || parts[0].Groups.End != 64 || parts[1].Groups.Start != 64 || parts[1].Groups.End != 85 || string(parts[0].Snapshot.Data) != "old-a" || string(parts[1].Snapshot.Data) != "old-b" {
				t.Fatalf("source ranges crossed: %+v", parts)
			}
		})
	}
}
