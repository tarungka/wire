package engine

import "errors"

var (
	// ErrTaskFailed indicates the task slot failed during execution.
	ErrTaskFailed = errors.New("engine: task failed")

	// ErrOperatorPanic indicates an operator panicked during processing.
	ErrOperatorPanic = errors.New("engine: operator panic")

	// ErrAlignmentTimeout indicates barrier alignment exceeded the drain timeout.
	ErrAlignmentTimeout = errors.New("engine: barrier alignment timeout")

	// ErrSideBufferFull indicates a per-input side buffer reached capacity.
	ErrSideBufferFull = errors.New("engine: side buffer full")

	// ErrInputClosed indicates the input channel was closed unexpectedly.
	ErrInputClosed = errors.New("engine: input channel closed")

	// ErrShutdownTimeout indicates the shutdown drain timeout was exceeded.
	ErrShutdownTimeout = errors.New("engine: shutdown drain timeout exceeded")

	// ErrMaxConsecutiveCheckpointFailures indicates the consecutive checkpoint
	// failure threshold was exceeded.
	ErrMaxConsecutiveCheckpointFailures = errors.New("engine: max consecutive checkpoint failures exceeded")

	// ErrCheckpointAborted indicates a checkpoint was aborted (e.g. due to timeout).
	ErrCheckpointAborted = errors.New("engine: checkpoint aborted")

	// ErrCheckpointFailureRateExceeded indicates the tolerable checkpoint
	// failure rate was exceeded.
	ErrCheckpointFailureRateExceeded = errors.New("engine: checkpoint failure rate exceeded")

	// ErrUnsupportedSchemaVersion indicates the checkpoint metadata uses an
	// unrecognized or unsupported schema version.
	ErrUnsupportedSchemaVersion = errors.New("engine: unsupported checkpoint schema version")

	// ErrSavepointIncompatible indicates the savepoint is incompatible with
	// the current job graph (e.g. missing stateful operators, key group mismatch).
	ErrSavepointIncompatible = errors.New("engine: savepoint incompatible with job graph")

	// ErrInvalidCheckpointMetadata indicates the checkpoint metadata fails
	// structural validation (e.g. missing fields, invalid ranges).
	ErrInvalidCheckpointMetadata = errors.New("engine: invalid checkpoint metadata")

	// ErrCheckpointAlreadyActive indicates a new checkpoint was rejected
	// because another checkpoint is currently in progress.
	ErrCheckpointAlreadyActive = errors.New("engine: checkpoint already active")

	// ErrBeginTransactionFailed indicates a transactional sink failed to begin a new transaction.
	ErrBeginTransactionFailed = errors.New("engine: transactional sink begin transaction failed")

	// ErrPreCommitFailed indicates a transactional sink failed during pre-commit.
	ErrPreCommitFailed = errors.New("engine: transactional sink pre-commit failed")

	// ErrCommitFailed indicates a transactional sink failed to commit a transaction.
	ErrCommitFailed = errors.New("engine: transactional sink commit failed")

	// ErrAbortFailed indicates a transactional sink failed to abort a transaction.
	ErrAbortFailed = errors.New("engine: transactional sink abort failed")

	// ErrTransient marks an error as transient (retryable). Wrap errors with
	// this sentinel so the error classifier routes them for retry:
	//   fmt.Errorf("connection reset: %w", ErrTransient)
	ErrTransient = errors.New("engine: transient error")

	// ErrFatal marks an error as fatal (should immediately fail the job).
	ErrFatal = errors.New("engine: fatal error")

	// ErrRetriesExhausted indicates the maximum retry count was reached
	// without a successful invocation.
	ErrRetriesExhausted = errors.New("engine: retries exhausted")
)
