package rpc

import (
	"errors"
	"fmt"
)

// ErrorCode represents a typed RPC error code per TRD Section 4.3.
type ErrorCode int32

// Transport errors (1000–1004).
const (
	ErrCodeInternalError      ErrorCode = 1000
	ErrCodeTimeout            ErrorCode = 1001
	ErrCodeInvalidRequest     ErrorCode = 1002
	ErrCodeUnknownMethod      ErrorCode = 1003
	ErrCodeSerializationError ErrorCode = 1004
)

// Job management errors (2000–2005).
const (
	ErrCodeUnknownJob        ErrorCode = 2000
	ErrCodeUnknownTask       ErrorCode = 2001
	ErrCodeInvalidTransition ErrorCode = 2002
	ErrCodeStaleEpoch        ErrorCode = 2003
	ErrCodeInvalidJobGraph   ErrorCode = 2004
	ErrCodeDuplicateTask     ErrorCode = 2005
)

// Checkpoint errors (3000–3003).
const (
	ErrCodeUnknownCheckpoint       ErrorCode = 3000
	ErrCodeCheckpointAlreadyFailed ErrorCode = 3001
	ErrCodeCheckpointInProgress    ErrorCode = 3002
	ErrCodeTaskNotRunning          ErrorCode = 3003
)

// Resource errors (4000–4001).
const (
	ErrCodeInsufficientSlots     ErrorCode = 4000
	ErrCodeInsufficientResources ErrorCode = 4001
)

// Worker lifecycle errors (5000–5001).
const (
	ErrCodeWorkerShuttingDown ErrorCode = 5000
	ErrCodeStateRestoreFailed ErrorCode = 5001
)

// Heartbeat errors (6000–6001).
const (
	ErrCodeHeartbeatTimeout ErrorCode = 6000
	ErrCodeWorkerLost       ErrorCode = 6001
)

// RPCError is the error envelope returned by RPC handlers.
type RPCError struct {
	Code         ErrorCode `codec:"c"`
	Name         string    `codec:"n"`
	Message      string    `codec:"m"`
	Retryable    bool      `codec:"r"`
	RetryAfterMs int64     `codec:"ra,omitempty"`
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d (%s): %s", e.Code, e.Name, e.Message)
}

// NewRPCError creates an RPCError with the given code and message.
func NewRPCError(code ErrorCode, message string) *RPCError {
	return &RPCError{
		Code:      code,
		Name:      ErrorCodeName(code),
		Message:   message,
		Retryable: IsRetryable(code),
	}
}

// ErrorCodeName returns the human-readable name for an error code.
func ErrorCodeName(code ErrorCode) string {
	switch code {
	case ErrCodeInternalError:
		return "INTERNAL_ERROR"
	case ErrCodeTimeout:
		return "TIMEOUT"
	case ErrCodeInvalidRequest:
		return "INVALID_REQUEST"
	case ErrCodeUnknownMethod:
		return "UNKNOWN_METHOD"
	case ErrCodeSerializationError:
		return "SERIALIZATION_ERROR"
	case ErrCodeUnknownJob:
		return "UNKNOWN_JOB"
	case ErrCodeUnknownTask:
		return "UNKNOWN_TASK"
	case ErrCodeInvalidTransition:
		return "INVALID_TRANSITION"
	case ErrCodeStaleEpoch:
		return "STALE_EPOCH"
	case ErrCodeInvalidJobGraph:
		return "INVALID_JOB_GRAPH"
	case ErrCodeDuplicateTask:
		return "DUPLICATE_TASK"
	case ErrCodeUnknownCheckpoint:
		return "UNKNOWN_CHECKPOINT"
	case ErrCodeCheckpointAlreadyFailed:
		return "CHECKPOINT_ALREADY_FAILED"
	case ErrCodeCheckpointInProgress:
		return "CHECKPOINT_IN_PROGRESS"
	case ErrCodeTaskNotRunning:
		return "TASK_NOT_RUNNING"
	case ErrCodeInsufficientSlots:
		return "INSUFFICIENT_SLOTS"
	case ErrCodeInsufficientResources:
		return "INSUFFICIENT_RESOURCES"
	case ErrCodeWorkerShuttingDown:
		return "WORKER_SHUTTING_DOWN"
	case ErrCodeStateRestoreFailed:
		return "STATE_RESTORE_FAILED"
	case ErrCodeHeartbeatTimeout:
		return "HEARTBEAT_TIMEOUT"
	case ErrCodeWorkerLost:
		return "WORKER_LOST"
	default:
		return "UNKNOWN"
	}
}

// IsRetryable returns true if the error code indicates a retryable error.
func IsRetryable(code ErrorCode) bool {
	switch code {
	case ErrCodeInternalError, ErrCodeTimeout, ErrCodeInsufficientSlots,
		ErrCodeInsufficientResources, ErrCodeCheckpointInProgress,
		ErrCodeHeartbeatTimeout:
		return true
	default:
		return false
	}
}

// Sentinel errors for common RPC failure modes.
var (
	ErrRPCTimeout           = errors.New("rpc: request timed out")
	ErrRPCClosed            = errors.New("rpc: connection closed")
	ErrRPCUnknownMethod     = errors.New("rpc: unknown method")
	ErrRPCEncodeFailed      = errors.New("rpc: encode failed")
	ErrRPCDecodeFailed      = errors.New("rpc: decode failed")
	ErrRPCPayloadTooLarge   = errors.New("rpc: payload exceeds maximum size")
	ErrHeartbeatContactLost = errors.New("rpc: coordinator contact lost")
)
