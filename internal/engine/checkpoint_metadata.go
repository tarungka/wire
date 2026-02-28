package engine

import (
	"encoding/json"
	"fmt"
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
	SinkTxns       []SinkTransaction `json:"sink_txns"`
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
	TaskID         string            `json:"task_id"`
	OperatorID     string            `json:"operator_id"`
	SubtaskIndex   int               `json:"subtask_index"`
	KeyGroupRange  KeyGroupRangeMeta `json:"key_group_range"`
	StatePath      string            `json:"state_path"`
	StateSizeBytes int64             `json:"state_size_bytes"`
	StateFiles     []string          `json:"state_files"`
	SourceOffsets  json.RawMessage   `json:"source_offsets,omitempty"`
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
	return &meta, nil
}
