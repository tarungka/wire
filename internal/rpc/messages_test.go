package rpc

import (
	"testing"

	"github.com/tarungka/wire/internal/protocol"
)

func TestTaskStatusString(t *testing.T) {
	tests := []struct {
		s    TaskStatus
		want string
	}{
		{TaskStatusUnknown, "Unknown"},
		{TaskStatusCreated, "Created"},
		{TaskStatusScheduled, "Scheduled"},
		{TaskStatusDeploying, "Deploying"},
		{TaskStatusRunning, "Running"},
		{TaskStatusFinishing, "Finishing"},
		{TaskStatusCanceling, "Canceling"},
		{TaskStatusFailed, "Failed"},
		{TaskStatusCanceled, "Canceled"},
		{TaskStatusRecovering, "Recovering"},
		{TaskStatusFinished, "Finished"},
		{TaskStatus(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("TaskStatus(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestOperatorTypeString(t *testing.T) {
	tests := []struct {
		o    OperatorType
		want string
	}{
		{OperatorTypeUnknown, "Unknown"},
		{OperatorTypeSource, "Source"},
		{OperatorTypeMap, "Map"},
		{OperatorTypeFlatMap, "FlatMap"},
		{OperatorTypeFilter, "Filter"},
		{OperatorTypeKeyBy, "KeyBy"},
		{OperatorTypeWindow, "Window"},
		{OperatorTypeReduce, "Reduce"},
		{OperatorTypeJoin, "Join"},
		{OperatorTypeProcess, "Process"},
		{OperatorTypeSink, "Sink"},
		{OperatorType(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.o.String(); got != tt.want {
			t.Errorf("OperatorType(%d).String() = %q, want %q", tt.o, got, tt.want)
		}
	}
}

func TestShuffleStrategyString(t *testing.T) {
	tests := []struct {
		s    ShuffleStrategy
		want string
	}{
		{ShuffleStrategyUnknown, "Unknown"},
		{ShuffleStrategyForward, "Forward"},
		{ShuffleStrategyHash, "Hash"},
		{ShuffleStrategyBroadcast, "Broadcast"},
		{ShuffleStrategyRebalance, "Rebalance"},
		{ShuffleStrategy(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("ShuffleStrategy(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestCheckpointTypeString(t *testing.T) {
	tests := []struct {
		c    CheckpointType
		want string
	}{
		{CheckpointTypeUnknown, "Unknown"},
		{CheckpointTypePeriodic, "Periodic"},
		{CheckpointTypeSavepoint, "Savepoint"},
		{CheckpointType(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("CheckpointType(%d).String() = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestRestartStrategyTypeString(t *testing.T) {
	tests := []struct {
		r    RestartStrategyType
		want string
	}{
		{RestartStrategyUnknown, "Unknown"},
		{RestartStrategyFixedDelay, "FixedDelay"},
		{RestartStrategyExponentialBackoff, "ExponentialBackoff"},
		{RestartStrategyNoRestart, "NoRestart"},
		{RestartStrategyType(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.r.String(); got != tt.want {
			t.Errorf("RestartStrategyType(%d).String() = %q, want %q", tt.r, got, tt.want)
		}
	}
}

func TestCommandTypeString(t *testing.T) {
	tests := []struct {
		c    CommandType
		want string
	}{
		{CommandTypeNone, "None"},
		{CommandTypeCancelJob, "CancelJob"},
		{CommandTypeDeployTask, "DeployTask"},
		{CommandTypeCancelTask, "CancelTask"},
		{CommandTypeTakeSnapshot, "TakeSnapshot"},
		{CommandTypeUpdateConfig, "UpdateConfig"},
		{CommandType(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("CommandType(%d).String() = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestDirectiveTypeString(t *testing.T) {
	tests := []struct {
		d    DirectiveType
		want string
	}{
		{DirectiveTypeNone, "None"},
		{DirectiveTypeCancelTask, "CancelTask"},
		{DirectiveTypeTriggerSavepoint, "TriggerSavepoint"},
		{DirectiveType(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.d.String(); got != tt.want {
			t.Errorf("DirectiveType(%d).String() = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestMsgpackRoundtrip tests that all request/response pairs survive msgpack serialization.
func TestMsgpackRoundtrip(t *testing.T) {
	t.Run("SubmitJob", func(t *testing.T) {
		req := SubmitJobRequest{
			JobID:   "job-1",
			JobName: "test-job",
			EpochID: 1,
			Graph: JobGraph{
				Operators: []OperatorDescriptor{
					{OperatorID: "op-1", Name: "source", Type: OperatorTypeSource, Parallelism: 2},
					{OperatorID: "op-2", Name: "sink", Type: OperatorTypeSink, Parallelism: 2},
				},
				Edges: []EdgeDescriptor{
					{SourceOperatorID: "op-1", TargetOperatorID: "op-2", Shuffle: ShuffleStrategyForward},
				},
			},
			Config: JobConfig{
				MaxParallelism: 4,
				RestartStrategy: RestartStrategy{
					Type:        RestartStrategyFixedDelay,
					MaxAttempts: 3,
					DelayMs:     1000,
				},
			},
		}

		data, err := protocol.EncodeMsgPack(&req)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var decoded SubmitJobRequest
		if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if decoded.JobID != req.JobID {
			t.Errorf("JobID = %q, want %q", decoded.JobID, req.JobID)
		}
		if len(decoded.Graph.Operators) != 2 {
			t.Errorf("Operators count = %d, want 2", len(decoded.Graph.Operators))
		}
		if decoded.Graph.Operators[0].Type != OperatorTypeSource {
			t.Errorf("Operator type = %d, want %d", decoded.Graph.Operators[0].Type, OperatorTypeSource)
		}

		resp := SubmitJobResponse{
			Accepted: true,
			TaskStatuses: []TaskDeploymentStatus{
				{TaskID: "t-1", Status: TaskStatusRunning},
			},
		}

		data, err = protocol.EncodeMsgPack(&resp)
		if err != nil {
			t.Fatalf("encode resp: %v", err)
		}

		var decodedResp SubmitJobResponse
		if err := protocol.DecodeMsgPack(data, &decodedResp); err != nil {
			t.Fatalf("decode resp: %v", err)
		}

		if !decodedResp.Accepted {
			t.Error("expected Accepted=true")
		}
	})

	t.Run("UpdateTaskStatus", func(t *testing.T) {
		req := UpdateTaskStatusRequest{
			JobID:   "job-1",
			TaskID:  "task-1",
			Status:  TaskStatusRunning,
			EpochID: 5,
			Metrics: &TaskMetrics{
				RecordsIn: 1000, RecordsOut: 900, BytesIn: 50000, BytesOut: 45000,
			},
		}

		data, err := protocol.EncodeMsgPack(&req)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var decoded UpdateTaskStatusRequest
		if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if decoded.Status != TaskStatusRunning {
			t.Errorf("Status = %d, want %d", decoded.Status, TaskStatusRunning)
		}
		if decoded.Metrics.RecordsIn != 1000 {
			t.Errorf("RecordsIn = %d, want 1000", decoded.Metrics.RecordsIn)
		}
	})

	t.Run("TriggerCheckpoint", func(t *testing.T) {
		req := TriggerCheckpointRequest{
			JobID:        "job-1",
			CheckpointID: 10,
			EpochID:      3,
			Type:         CheckpointTypePeriodic,
			Options:      CheckpointOptions{TimeoutMs: 5000},
			Timestamp:    1000000,
		}

		data, err := protocol.EncodeMsgPack(&req)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var decoded TriggerCheckpointRequest
		if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if decoded.CheckpointID != 10 {
			t.Errorf("CheckpointID = %d, want 10", decoded.CheckpointID)
		}
		if decoded.Type != CheckpointTypePeriodic {
			t.Errorf("Type = %d, want %d", decoded.Type, CheckpointTypePeriodic)
		}
	})

	t.Run("AcknowledgeCheckpoint", func(t *testing.T) {
		req := AcknowledgeCheckpointRequest{
			JobID:        "job-1",
			TaskID:       "task-1",
			CheckpointID: 10,
			EpochID:      3,
			State:        &StateHandle{TaskID: "task-1", Path: "/tmp/chk-10", SizeBytes: 4096},
			Metrics:      &AckCheckpointMetrics{StateSizeBytes: 4096, DurationMs: 150},
		}

		data, err := protocol.EncodeMsgPack(&req)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var decoded AcknowledgeCheckpointRequest
		if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if decoded.State.Path != "/tmp/chk-10" {
			t.Errorf("State.Path = %q, want %q", decoded.State.Path, "/tmp/chk-10")
		}
	})

	t.Run("RequestTaskSlots", func(t *testing.T) {
		req := RequestTaskSlotsRequest{
			JobID:         "job-1",
			RequiredSlots: 4,
			MemoryMB:      1024,
		}

		data, err := protocol.EncodeMsgPack(&req)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var decoded RequestTaskSlotsRequest
		if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if decoded.RequiredSlots != 4 {
			t.Errorf("RequiredSlots = %d, want 4", decoded.RequiredSlots)
		}
	})

	t.Run("ResourceReport", func(t *testing.T) {
		rr := ResourceReport{
			CPUUsagePercent:  75.5,
			MemoryUsedBytes:  1024 * 1024 * 512,
			MemoryTotalBytes: 1024 * 1024 * 1024,
			DiskUsedBytes:    1024 * 1024 * 100,
			DiskTotalBytes:   1024 * 1024 * 500,
			GoroutineCount:   42,
		}

		data, err := protocol.EncodeMsgPack(&rr)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var decoded ResourceReport
		if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if decoded.CPUUsagePercent != 75.5 {
			t.Errorf("CPUUsagePercent = %f, want 75.5", decoded.CPUUsagePercent)
		}
		if decoded.GoroutineCount != 42 {
			t.Errorf("GoroutineCount = %d, want 42", decoded.GoroutineCount)
		}
		if decoded.MemoryUsedBytes != 1024*1024*512 {
			t.Errorf("MemoryUsedBytes = %d, want %d", decoded.MemoryUsedBytes, 1024*1024*512)
		}
	})

	t.Run("Heartbeat", func(t *testing.T) {
		req := HeartbeatRequest{
			WorkerID:  "worker-1",
			EpochID:   7,
			Timestamp: 1700000000,
			Load:      &WorkerLoad{CPUUsage: 0.5, MemoryUsage: 0.7, ActiveSlots: 3, TotalSlots: 8},
			Resources: &ResourceReport{
				CPUUsagePercent: 50.0, MemoryUsedBytes: 1024, MemoryTotalBytes: 2048,
				GoroutineCount: 10,
			},
			Tasks: []RunningTaskSummary{
				{
					TaskID: "t-1", JobID: "j-1", Status: TaskStatusRunning, UptimeMs: 60000,
					Metrics: &TaskMetrics{RecordsIn: 500, RecordsOut: 480},
				},
			},
		}

		data, err := protocol.EncodeMsgPack(&req)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var decoded HeartbeatRequest
		if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if decoded.WorkerID != "worker-1" {
			t.Errorf("WorkerID = %q, want %q", decoded.WorkerID, "worker-1")
		}
		if decoded.Timestamp != 1700000000 {
			t.Errorf("Timestamp = %d, want 1700000000", decoded.Timestamp)
		}
		if decoded.Load.CPUUsage != 0.5 {
			t.Errorf("CPUUsage = %f, want 0.5", decoded.Load.CPUUsage)
		}
		if decoded.Resources == nil {
			t.Fatal("expected non-nil Resources")
		}
		if decoded.Resources.CPUUsagePercent != 50.0 {
			t.Errorf("Resources.CPUUsagePercent = %f, want 50.0", decoded.Resources.CPUUsagePercent)
		}
		if len(decoded.Tasks) != 1 {
			t.Fatalf("Tasks count = %d, want 1", len(decoded.Tasks))
		}
		if decoded.Tasks[0].Status != TaskStatusRunning {
			t.Errorf("Task status = %d, want %d", decoded.Tasks[0].Status, TaskStatusRunning)
		}
		if decoded.Tasks[0].Metrics == nil {
			t.Fatal("expected non-nil task Metrics")
		}
		if decoded.Tasks[0].Metrics.RecordsIn != 500 {
			t.Errorf("Metrics.RecordsIn = %d, want 500", decoded.Tasks[0].Metrics.RecordsIn)
		}

		resp := HeartbeatResponse{
			Accepted: true,
			EpochID:  7,
			Commands: []WorkerCommand{
				{Type: CommandTypeCancelTask, JobID: "j-1", TaskID: "t-1"},
			},
		}

		data, err = protocol.EncodeMsgPack(&resp)
		if err != nil {
			t.Fatalf("encode resp: %v", err)
		}

		var decodedResp HeartbeatResponse
		if err := protocol.DecodeMsgPack(data, &decodedResp); err != nil {
			t.Fatalf("decode resp: %v", err)
		}

		if !decodedResp.Accepted {
			t.Error("expected Accepted=true")
		}
		if len(decodedResp.Commands) != 1 {
			t.Fatalf("Commands count = %d, want 1", len(decodedResp.Commands))
		}
		if decodedResp.Commands[0].Type != CommandTypeCancelTask {
			t.Errorf("Command type = %d, want %d", decodedResp.Commands[0].Type, CommandTypeCancelTask)
		}
	})
}

func TestZeroValueEncoding(t *testing.T) {
	// Verify that zero-value structs roundtrip correctly.
	var req SubmitJobRequest
	data, err := protocol.EncodeMsgPack(&req)
	if err != nil {
		t.Fatalf("encode zero value: %v", err)
	}

	var decoded SubmitJobRequest
	if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
		t.Fatalf("decode zero value: %v", err)
	}

	if decoded.JobID != "" {
		t.Errorf("expected empty JobID, got %q", decoded.JobID)
	}
}
