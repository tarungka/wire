package engine

import (
	"context"

	"github.com/rs/zerolog"
)

// RunOperatorChain is a thin exported wrapper around the unexported
// runOperatorChain function. It enables the SDK embedded executor to
// run operator chains without going through TaskSlot.
func RunOperatorChain(
	ctx context.Context,
	operators []Operator,
	inputCh <-chan Event,
	controlCh <-chan ControlMsg,
	outputCh chan<- OutputMsg,
	aligner *BarrierAligner,
	numInputs int,
	metrics CheckpointMetrics,
	log zerolog.Logger,
	txnSink TransactionalSink,
	ackFn func(checkpointID uint64),
	errorConfigs []ErrorHandlerConfig,
	dlqCh chan<- DLQEvent,
	errMetrics ErrorMetrics,
) error {
	return runOperatorChain(ctx, operators, inputCh, controlCh, outputCh, aligner, numInputs, metrics, log, txnSink, ackFn, errorConfigs, dlqCh, errMetrics)
}

// RunSourceReader is a thin exported wrapper around the unexported
// runSourceReader function.
func RunSourceReader(
	ctx context.Context,
	source SourceOperator,
	strategy WatermarkStrategy,
	eventCh chan<- Event,
	controlCh chan<- ControlMsg,
	log zerolog.Logger,
) error {
	return runSourceReader(ctx, source, strategy, eventCh, controlCh, log)
}
