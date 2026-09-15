package coordinator

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

// checkpointManifest builds the portable inventory from the topology captured
// at trigger time, never from a newer deployment. Inventory keys must exactly
// cover the checkpoint's assigned tasks. Replica addresses remain in the
// coordinator record and are not interpreted as filesystem paths.
func checkpointManifest(job *JobMeta, checkpoint CheckpointMeta, inventory map[string]engine.TaskMeta, completed time.Time) (*engine.CheckpointMetadata, error) {
	if job == nil || job.ID != checkpoint.JobID || checkpoint.ID == 0 || checkpoint.ID > math.MaxInt64 {
		return nil, fmt.Errorf("invalid checkpoint manifest identity")
	}
	if len(inventory) != len(checkpoint.Tasks) || len(checkpoint.TaskDescriptors) != len(checkpoint.Tasks) {
		return nil, fmt.Errorf("checkpoint manifest does not cover assigned tasks")
	}
	m := &engine.CheckpointMetadata{SchemaVersion: engine.CurrentSchemaVersion, Type: engine.CheckpointType, CheckpointID: int64(checkpoint.ID), JobID: job.ID, JobName: job.Name, TriggerTime: checkpoint.Timestamp, CompletionTime: completed, DurationMs: completed.Sub(checkpoint.Timestamp).Milliseconds(), JobGraph: engine.JobGraphMeta{NumKeyGroups: checkpoint.NumKeyGroups}}
	if checkpoint.SavepointID != "" {
		m.Type = engine.SavepointType
	}
	operators := make(map[string]engine.OperatorMeta)
	seen := make(map[string]bool)
	for _, desc := range checkpoint.TaskDescriptors {
		if seen[desc.TaskID] || checkpoint.Tasks[desc.TaskID] == "" {
			return nil, fmt.Errorf("duplicate or unassigned task descriptor %q", desc.TaskID)
		}
		seen[desc.TaskID] = true
		task, ok := inventory[desc.TaskID]
		if !ok || task.TaskID != desc.TaskID {
			return nil, fmt.Errorf("missing task inventory %q", desc.TaskID)
		}
		// The physical primary can be the first non-source operator, not the
		// first chain member. All other operators share that primary's tasks.
		primaryFound := false
		for _, op := range desc.OperatorChain {
			meta := engine.OperatorMeta{OperatorID: op.OperatorID, Type: strings.ToLower(op.Type.String()), Parallelism: int(desc.Parallelism)}
			if op.OperatorID != desc.OperatorID {
				primary := desc.OperatorID
				meta.ChainedTo = &primary
			} else {
				primaryFound = true
			}
			if previous, exists := operators[op.OperatorID]; exists && !reflect.DeepEqual(previous, meta) {
				return nil, fmt.Errorf("inconsistent operator topology %q", op.OperatorID)
			}
			operators[op.OperatorID] = meta
		}
		if !primaryFound {
			return nil, fmt.Errorf("task %q has no primary operator", desc.TaskID)
		}
		task.OperatorID = desc.OperatorID
		task.SubtaskIndex = int(desc.SubtaskIndex)
		task.KeyGroupRange = engine.KeyGroupRangeMeta{Start: int(desc.KeyGroup.Start), End: int(desc.KeyGroup.End) + 1}
		if task.SinkPrepared {
			if task.SinkCommittedCheckpoint > checkpoint.ID {
				return nil, fmt.Errorf("sink commit boundary is ahead of checkpoint")
			}
			sinkID := ""
			for _, op := range desc.OperatorChain {
				if strings.ToLower(op.Type.String()) == "sink" {
					sinkID = op.OperatorID
				}
			}
			if sinkID == "" {
				return nil, fmt.Errorf("prepared transaction without sink operator")
			}
			m.SinkTxns = append(m.SinkTxns, engine.SinkTransaction{TaskID: task.TaskID, OperatorID: sinkID, CommittedCheckpoint: int64(task.SinkCommittedCheckpoint), TransactionState: "PRE_COMMITTED"})
		}
		m.Tasks = append(m.Tasks, task)
	}
	for _, op := range operators {
		m.JobGraph.Operators = append(m.JobGraph.Operators, op)
	}
	sort.Slice(m.JobGraph.Operators, func(i, j int) bool { return m.JobGraph.Operators[i].OperatorID < m.JobGraph.Operators[j].OperatorID })
	sort.Slice(m.Tasks, func(i, j int) bool { return m.Tasks[i].TaskID < m.Tasks[j].TaskID })
	sort.Slice(m.SinkTxns, func(i, j int) bool { return m.SinkTxns[i].TaskID < m.SinkTxns[j].TaskID })
	if err := m.ValidateComplete(); err != nil {
		return nil, err
	}
	return m, nil
}

// CheckpointManifestKey stores JSON in the same atomic metadata batch as completion.
func CheckpointManifestKey(jobID string, id uint64) []byte {
	return []byte(fmt.Sprintf("jobs/%s/checkpoints/chk-%d/metadata.json", jobID, id))
}
