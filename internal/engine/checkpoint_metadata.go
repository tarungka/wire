package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Schema version and checkpoint type constants.
const (
	CurrentSchemaVersion = 1
	CheckpointType       = "checkpoint"
	SavepointType        = "savepoint"
)

// CheckpointMetadata is the top-level structure serialized as metadata.json
// for each checkpoint or savepoint. It serves as the "table of contents" for
// all state persisted during a checkpoint.
type CheckpointMetadata struct {
	SchemaVersion  int               `json:"schema_version"`
	Type           string            `json:"type"`
	CheckpointID   int64             `json:"checkpoint_id"`
	JobID          string            `json:"job_id"`
	JobName        string            `json:"job_name"`
	TriggerTime    time.Time         `json:"trigger_time"`
	CompletionTime time.Time         `json:"completion_time"`
	DurationMs     int64             `json:"duration_ms"`
	JobGraph       JobGraphMeta      `json:"job_graph"`
	Tasks          []TaskMeta        `json:"tasks"`
	SinkTxns       []SinkTransaction `json:"sink_transactions"`
}

// JobGraphMeta describes the logical job graph at checkpoint time.
type JobGraphMeta struct {
	NumKeyGroups int            `json:"num_key_groups"`
	Operators    []OperatorMeta `json:"operators"`
}

// OperatorMeta describes a single operator in the job graph.
type OperatorMeta struct {
	OperatorID  string  `json:"operator_id"`
	Type        string  `json:"type"`
	Parallelism int     `json:"parallelism"`
	ChainedTo   *string `json:"chained_to"`
}

// TaskMeta describes a single task's checkpoint state.
type TaskMeta struct {
	TaskID           string            `json:"task_id"`
	OperatorID       string            `json:"operator_id"`
	SubtaskIndex     int               `json:"subtask_index"`
	KeyGroupRange    KeyGroupRangeMeta `json:"key_group_range"`
	StatePath        string            `json:"state_path"`
	StateSizeBytes   int64             `json:"state_size_bytes"`
	StateFiles       []string          `json:"state_files"`
	SourceOffsets    json.RawMessage   `json:"source_offsets,omitempty"`
	StateBackendType string            `json:"state_backend_type,omitempty"`
}

// KeyGroupRangeMeta represents a key group range in checkpoint metadata.
// This is deliberately separate from keygroup.KeyGroupRange to decouple the
// persistence schema from internal types.
type KeyGroupRangeMeta struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SinkTransaction records the transaction state for a sink task at
// checkpoint time.
type SinkTransaction struct {
	TaskID              string  `json:"task_id"`
	OperatorID          string  `json:"operator_id"`
	CommittedCheckpoint int64   `json:"committed_checkpoint"`
	TransactionID       *string `json:"transaction_id"`
	TransactionState    string  `json:"transaction_state,omitempty"` // "ACTIVE", "PRE_COMMITTED", "COMMITTED"
}

// Validate checks structural invariants on the checkpoint metadata. It
// collects all errors before returning so callers see the full picture.
func (m *CheckpointMetadata) Validate() error {
	var errs []string

	// Type must be checkpoint or savepoint.
	if m.Type != CheckpointType && m.Type != SavepointType {
		errs = append(errs, fmt.Sprintf("invalid type %q, must be %q or %q", m.Type, CheckpointType, SavepointType))
	}

	// JobID must be non-empty.
	if m.JobID == "" {
		errs = append(errs, "job_id must not be empty")
	}

	// NumKeyGroups must be positive.
	if m.JobGraph.NumKeyGroups <= 0 {
		errs = append(errs, fmt.Sprintf("num_key_groups must be positive, got %d", m.JobGraph.NumKeyGroups))
	}

	// Validate operators: positive parallelism, non-empty IDs, no duplicates.
	operatorIDs := make(map[string]bool, len(m.JobGraph.Operators))
	for i, op := range m.JobGraph.Operators {
		if op.OperatorID == "" {
			errs = append(errs, fmt.Sprintf("operators[%d]: operator_id must not be empty", i))
		} else if operatorIDs[op.OperatorID] {
			errs = append(errs, fmt.Sprintf("operators[%d]: duplicate operator_id %q", i, op.OperatorID))
		} else {
			operatorIDs[op.OperatorID] = true
		}

		if op.Parallelism <= 0 {
			errs = append(errs, fmt.Sprintf("operators[%d] (%s): parallelism must be positive, got %d", i, op.OperatorID, op.Parallelism))
		}
	}

	// Validate tasks: non-empty IDs, no duplicates, operator reference integrity, key group ranges.
	taskIDs := make(map[string]bool, len(m.Tasks))
	for i, task := range m.Tasks {
		if task.TaskID == "" {
			errs = append(errs, fmt.Sprintf("tasks[%d]: task_id must not be empty", i))
		} else if taskIDs[task.TaskID] {
			errs = append(errs, fmt.Sprintf("tasks[%d]: duplicate task_id %q", i, task.TaskID))
		} else {
			taskIDs[task.TaskID] = true
		}

		if task.OperatorID == "" {
			errs = append(errs, fmt.Sprintf("tasks[%d] (%s): operator_id must not be empty", i, task.TaskID))
		} else if !operatorIDs[task.OperatorID] {
			errs = append(errs, fmt.Sprintf("tasks[%d] (%s): references unknown operator_id %q", i, task.TaskID, task.OperatorID))
		}

		// Key group range: 0 <= start < end <= num_key_groups.
		kg := task.KeyGroupRange
		if kg.Start < 0 {
			errs = append(errs, fmt.Sprintf("tasks[%d] (%s): key_group_range start must be >= 0, got %d", i, task.TaskID, kg.Start))
		}
		if kg.Start >= kg.End {
			errs = append(errs, fmt.Sprintf("tasks[%d] (%s): key_group_range start (%d) must be < end (%d)", i, task.TaskID, kg.Start, kg.End))
		}
		if m.JobGraph.NumKeyGroups > 0 && kg.End > m.JobGraph.NumKeyGroups {
			errs = append(errs, fmt.Sprintf("tasks[%d] (%s): key_group_range end (%d) exceeds num_key_groups (%d)", i, task.TaskID, kg.End, m.JobGraph.NumKeyGroups))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidCheckpointMetadata, strings.Join(errs, "; "))
}

// MarshalCheckpointMetadata serializes checkpoint metadata to pretty-printed
// JSON with 2-space indentation.
func MarshalCheckpointMetadata(meta *CheckpointMetadata) ([]byte, error) {
	return json.MarshalIndent(meta, "", "  ")
}

// UnmarshalCheckpointMetadata deserializes checkpoint metadata from JSON.
// It performs a two-pass unmarshal: first extracting and validating the
// schema_version, then unmarshaling the full structure.
func UnmarshalCheckpointMetadata(data []byte) (*CheckpointMetadata, error) {
	// First pass: extract schema version.
	var version struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return nil, fmt.Errorf("checkpoint metadata: invalid JSON: %w", err)
	}

	if version.SchemaVersion != CurrentSchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d",
			ErrUnsupportedSchemaVersion, version.SchemaVersion, CurrentSchemaVersion)
	}

	// Second pass: full unmarshal.
	var meta CheckpointMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("checkpoint metadata: %w", err)
	}

	if err := meta.Validate(); err != nil {
		return nil, err
	}
	return &meta, nil
}
