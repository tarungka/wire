package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// testCheckpointMetadata returns a fully populated test fixture.
func testCheckpointMetadata() *CheckpointMetadata {
	chainedTo := "source-1"
	txnID := "txn-abc-123"
	return &CheckpointMetadata{
		SchemaVersion:  CurrentSchemaVersion,
		Type:           CheckpointType,
		CheckpointID:   42,
		JobID:          "job-001",
		JobName:        "word-count",
		TriggerTime:    time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
		CompletionTime: time.Date(2024, 1, 15, 12, 0, 5, 0, time.UTC),
		DurationMs:     5000,
		JobGraph: JobGraphMeta{
			NumKeyGroups: 128,
			Operators: []OperatorMeta{
				{OperatorID: "source-1", Type: "source", Parallelism: 2, ChainedTo: nil},
				{OperatorID: "map-1", Type: "map", Parallelism: 4, ChainedTo: &chainedTo},
				{OperatorID: "keyby-1", Type: "keyby", Parallelism: 4, ChainedTo: nil},
				{OperatorID: "window-1", Type: "window", Parallelism: 4, ChainedTo: nil},
				{OperatorID: "sink-1", Type: "sink", Parallelism: 2, ChainedTo: nil},
			},
		},
		Tasks: []TaskMeta{
			{
				TaskID:         "source-1-0",
				OperatorID:     "source-1",
				SubtaskIndex:   0,
				KeyGroupRange:  KeyGroupRangeMeta{Start: 0, End: 64},
				StatePath:      "task-0-state/",
				StateSizeBytes: 1024,
				StateFiles:     []string{"offsets.bin"},
				SourceOffsets:  json.RawMessage(`{"topic":"clicks","partition":0,"offset":1500}`),
			},
			{
				TaskID:         "keyby-1-0",
				OperatorID:     "keyby-1",
				SubtaskIndex:   0,
				KeyGroupRange:  KeyGroupRangeMeta{Start: 0, End: 32},
				StatePath:      "task-1-state/",
				StateSizeBytes: 2048,
				StateFiles:     []string{"keys.bin", "values.bin"},
			},
		},
		SinkTxns: []SinkTransaction{
			{
				TaskID:              "sink-1-0",
				OperatorID:          "sink-1",
				CommittedCheckpoint: 41,
				TransactionID:       &txnID,
			},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	meta := testCheckpointMetadata()

	data, err := MarshalCheckpointMetadata(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := UnmarshalCheckpointMetadata(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify top-level fields.
	if got.SchemaVersion != meta.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, meta.SchemaVersion)
	}
	if got.Type != meta.Type {
		t.Errorf("Type = %q, want %q", got.Type, meta.Type)
	}
	if got.CheckpointID != meta.CheckpointID {
		t.Errorf("CheckpointID = %d, want %d", got.CheckpointID, meta.CheckpointID)
	}
	if got.JobID != meta.JobID {
		t.Errorf("JobID = %q, want %q", got.JobID, meta.JobID)
	}
	if got.JobName != meta.JobName {
		t.Errorf("JobName = %q, want %q", got.JobName, meta.JobName)
	}
	if !got.TriggerTime.Equal(meta.TriggerTime) {
		t.Errorf("TriggerTime = %v, want %v", got.TriggerTime, meta.TriggerTime)
	}
	if !got.CompletionTime.Equal(meta.CompletionTime) {
		t.Errorf("CompletionTime = %v, want %v", got.CompletionTime, meta.CompletionTime)
	}
	if got.DurationMs != meta.DurationMs {
		t.Errorf("DurationMs = %d, want %d", got.DurationMs, meta.DurationMs)
	}

	// Verify job graph.
	if got.JobGraph.NumKeyGroups != meta.JobGraph.NumKeyGroups {
		t.Errorf("NumKeyGroups = %d, want %d", got.JobGraph.NumKeyGroups, meta.JobGraph.NumKeyGroups)
	}
	if len(got.JobGraph.Operators) != len(meta.JobGraph.Operators) {
		t.Fatalf("Operators len = %d, want %d", len(got.JobGraph.Operators), len(meta.JobGraph.Operators))
	}

	// Verify tasks.
	if len(got.Tasks) != len(meta.Tasks) {
		t.Fatalf("Tasks len = %d, want %d", len(got.Tasks), len(meta.Tasks))
	}
	if got.Tasks[0].TaskID != meta.Tasks[0].TaskID {
		t.Errorf("Tasks[0].TaskID = %q, want %q", got.Tasks[0].TaskID, meta.Tasks[0].TaskID)
	}
	if got.Tasks[0].StateSizeBytes != meta.Tasks[0].StateSizeBytes {
		t.Errorf("Tasks[0].StateSizeBytes = %d, want %d", got.Tasks[0].StateSizeBytes, meta.Tasks[0].StateSizeBytes)
	}

	// Verify sink transactions.
	if len(got.SinkTxns) != len(meta.SinkTxns) {
		t.Fatalf("SinkTxns len = %d, want %d", len(got.SinkTxns), len(meta.SinkTxns))
	}
	if got.SinkTxns[0].CommittedCheckpoint != meta.SinkTxns[0].CommittedCheckpoint {
		t.Errorf("SinkTxns[0].CommittedCheckpoint = %d, want %d",
			got.SinkTxns[0].CommittedCheckpoint, meta.SinkTxns[0].CommittedCheckpoint)
	}
	if got.SinkTxns[0].TransactionID == nil || *got.SinkTxns[0].TransactionID != *meta.SinkTxns[0].TransactionID {
		t.Errorf("SinkTxns[0].TransactionID = %v, want %v",
			got.SinkTxns[0].TransactionID, meta.SinkTxns[0].TransactionID)
	}
}

func TestRoundTripSavepoint(t *testing.T) {
	meta := testCheckpointMetadata()
	meta.Type = SavepointType
	// Test mixed nullable: ChainedTo nil on first, non-nil on second operator.
	meta.SinkTxns[0].TransactionID = nil

	data, err := MarshalCheckpointMetadata(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := UnmarshalCheckpointMetadata(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != SavepointType {
		t.Errorf("Type = %q, want %q", got.Type, SavepointType)
	}
	if got.SinkTxns[0].TransactionID != nil {
		t.Errorf("TransactionID = %v, want nil", got.SinkTxns[0].TransactionID)
	}
}

func TestNullableFields(t *testing.T) {
	meta := testCheckpointMetadata()
	// ChainedTo nil on first operator.
	meta.JobGraph.Operators[0].ChainedTo = nil

	data, err := MarshalCheckpointMetadata(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// nil *string → JSON null.
	raw := string(data)
	// The source-1 operator should have "chained_to": null.
	if !strings.Contains(raw, `"chained_to": null`) {
		t.Errorf("expected null for chained_to, got:\n%s", raw)
	}

	// SourceOffsets nil → field omitted.
	if meta.Tasks[1].SourceOffsets != nil {
		t.Fatal("precondition: Tasks[1].SourceOffsets should be nil in fixture")
	}
	// Marshal the second task and check source_offsets is absent.
	taskJSON, err := json.Marshal(meta.Tasks[1])
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	if strings.Contains(string(taskJSON), "source_offsets") {
		t.Errorf("expected source_offsets to be omitted when nil, got: %s", taskJSON)
	}
}

func TestSourceOffsetsRawMessage(t *testing.T) {
	meta := testCheckpointMetadata()

	data, err := MarshalCheckpointMetadata(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := UnmarshalCheckpointMetadata(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// json.MarshalIndent reformats the RawMessage with indentation, so we
	// compare the decoded values rather than raw strings.
	var original, decoded map[string]interface{}
	if err := json.Unmarshal(meta.Tasks[0].SourceOffsets, &original); err != nil {
		t.Fatalf("unmarshal original: %v", err)
	}
	if err := json.Unmarshal(got.Tasks[0].SourceOffsets, &decoded); err != nil {
		t.Fatalf("unmarshal decoded: %v", err)
	}

	// Verify key fields are preserved.
	if decoded["topic"] != original["topic"] {
		t.Errorf("topic = %v, want %v", decoded["topic"], original["topic"])
	}
	if decoded["partition"] != original["partition"] {
		t.Errorf("partition = %v, want %v", decoded["partition"], original["partition"])
	}
	if decoded["offset"] != original["offset"] {
		t.Errorf("offset = %v, want %v", decoded["offset"], original["offset"])
	}
}

func TestUnsupportedVersion(t *testing.T) {
	data := []byte(`{"schema_version": 99, "type": "checkpoint"}`)

	_, err := UnmarshalCheckpointMetadata(data)
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Errorf("error = %v, want ErrUnsupportedSchemaVersion", err)
	}
}

func TestMissingVersion(t *testing.T) {
	// schema_version defaults to 0 when absent.
	data := []byte(`{"type": "checkpoint"}`)

	_, err := UnmarshalCheckpointMetadata(data)
	if err == nil {
		t.Fatal("expected error for missing version")
	}
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Errorf("error = %v, want ErrUnsupportedSchemaVersion", err)
	}
}

func TestInvalidJSON(t *testing.T) {
	_, err := UnmarshalCheckpointMetadata([]byte(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error = %v, want message containing 'invalid JSON'", err)
	}
}

func TestTimeFormat(t *testing.T) {
	meta := testCheckpointMetadata()

	data, err := MarshalCheckpointMetadata(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, "2024-01-15T12:00:00Z") {
		t.Errorf("expected RFC 3339 time format, got:\n%s", raw)
	}
	if !strings.Contains(raw, "2024-01-15T12:00:05Z") {
		t.Errorf("expected RFC 3339 completion time, got:\n%s", raw)
	}
}

func TestEmptyTasks(t *testing.T) {
	meta := testCheckpointMetadata()
	meta.Tasks = []TaskMeta{}
	meta.SinkTxns = []SinkTransaction{}

	data, err := MarshalCheckpointMetadata(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	// Empty slice should serialize as [], not null.
	if !strings.Contains(raw, `"tasks": []`) {
		t.Errorf("expected tasks: [], got:\n%s", raw)
	}
	if !strings.Contains(raw, `"sink_txns": []`) {
		t.Errorf("expected sink_txns: [], got:\n%s", raw)
	}
}

func TestPrettyPrinted(t *testing.T) {
	meta := testCheckpointMetadata()

	data, err := MarshalCheckpointMetadata(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, "\n") {
		t.Error("expected newlines in pretty-printed output")
	}
	if !strings.Contains(raw, "  ") {
		t.Error("expected 2-space indentation in pretty-printed output")
	}
}
