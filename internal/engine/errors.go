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
)
