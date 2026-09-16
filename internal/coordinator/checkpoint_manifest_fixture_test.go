package coordinator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func manifestTaskDescriptors(ids ...string) []rpc.TaskDescriptor {
	var tasks []rpc.TaskDescriptor
	for i, id := range ids {
		tasks = append(tasks, rpc.TaskDescriptor{TaskID: id, OperatorID: "operator", SubtaskIndex: int32(i), Parallelism: int32(len(ids)), NumKeyGroups: 128, KeyGroup: rpc.KeyGroupRange{Start: int32(i * 128 / len(ids)), End: int32((i+1)*128/len(ids) - 1)}, OperatorChain: []rpc.OperatorDescriptor{{OperatorID: "operator", Type: rpc.OperatorTypeMap, Parallelism: int32(len(ids))}}})
	}
	return tasks
}

func manifestState(t *testing.T, id, location string) *rpc.StateHandle {
	t.Helper()
	data, err := json.Marshal(engine.TaskMeta{TaskID: id, StatePath: id, StateFiles: []string{"checkpoint.archive"}, StateSizeBytes: 1, StateSHA256: map[string]string{"checkpoint.archive": strings.Repeat("0", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	return &rpc.StateHandle{TaskID: id, Path: location, Manifest: data}
}
