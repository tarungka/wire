package rpc

import (
	"testing"
)

func TestErrorCodeName(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want string
	}{
		{ErrCodeInternalError, "INTERNAL_ERROR"},
		{ErrCodeTimeout, "TIMEOUT"},
		{ErrCodeInvalidRequest, "INVALID_REQUEST"},
		{ErrCodeUnknownMethod, "UNKNOWN_METHOD"},
		{ErrCodeSerializationError, "SERIALIZATION_ERROR"},
		{ErrCodeUnknownJob, "UNKNOWN_JOB"},
		{ErrCodeUnknownTask, "UNKNOWN_TASK"},
		{ErrCodeInvalidTransition, "INVALID_TRANSITION"},
		{ErrCodeStaleEpoch, "STALE_EPOCH"},
		{ErrCodeInvalidJobGraph, "INVALID_JOB_GRAPH"},
		{ErrCodeDuplicateTask, "DUPLICATE_TASK"},
		{ErrCodeUnknownCheckpoint, "UNKNOWN_CHECKPOINT"},
		{ErrCodeCheckpointAlreadyFailed, "CHECKPOINT_ALREADY_FAILED"},
		{ErrCodeCheckpointInProgress, "CHECKPOINT_IN_PROGRESS"},
		{ErrCodeTaskNotRunning, "TASK_NOT_RUNNING"},
		{ErrCodeInsufficientSlots, "INSUFFICIENT_SLOTS"},
		{ErrCodeInsufficientResources, "INSUFFICIENT_RESOURCES"},
		{ErrCodeWorkerShuttingDown, "WORKER_SHUTTING_DOWN"},
		{ErrCodeStateRestoreFailed, "STATE_RESTORE_FAILED"},
		{ErrorCode(9999), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := ErrorCodeName(tt.code)
			if got != tt.want {
				t.Errorf("ErrorCodeName(%d) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestIsRetryable(t *testing.T) {
	retryable := []ErrorCode{
		ErrCodeInternalError,
		ErrCodeTimeout,
		ErrCodeInsufficientSlots,
		ErrCodeInsufficientResources,
		ErrCodeCheckpointInProgress,
	}
	for _, code := range retryable {
		if !IsRetryable(code) {
			t.Errorf("expected IsRetryable(%d) = true for %s", code, ErrorCodeName(code))
		}
	}

	nonRetryable := []ErrorCode{
		ErrCodeInvalidRequest,
		ErrCodeUnknownMethod,
		ErrCodeUnknownJob,
		ErrCodeInvalidTransition,
		ErrCodeStaleEpoch,
		ErrCodeWorkerShuttingDown,
		ErrCodeStateRestoreFailed,
	}
	for _, code := range nonRetryable {
		if IsRetryable(code) {
			t.Errorf("expected IsRetryable(%d) = false for %s", code, ErrorCodeName(code))
		}
	}
}

func TestRPCErrorInterface(t *testing.T) {
	err := NewRPCError(ErrCodeTimeout, "connection timed out")

	got := err.Error()
	want := "rpc error 1001 (TIMEOUT): connection timed out"
	if got != want {
		t.Errorf("RPCError.Error() = %q, want %q", got, want)
	}

	if !err.Retryable {
		t.Error("expected TIMEOUT error to be retryable")
	}

	if err.Name != "TIMEOUT" {
		t.Errorf("expected Name = TIMEOUT, got %s", err.Name)
	}
}

func TestNewRPCError(t *testing.T) {
	err := NewRPCError(ErrCodeUnknownJob, "job not found")
	if err.Code != ErrCodeUnknownJob {
		t.Errorf("Code = %d, want %d", err.Code, ErrCodeUnknownJob)
	}
	if err.Retryable {
		t.Error("UNKNOWN_JOB should not be retryable")
	}
}
