package engine

import "github.com/tarungka/wire/internal/observability"

// ErrorMetrics abstracts error-handling metrics collection.
// Implementations may bridge to Prometheus or other systems.
type ErrorMetrics interface {
	// IncErrorTotal counts every failed operator invocation, including retries.
	// A single event that fails 3 times (initial + 2 retries) increments this
	// counter 3 times. This reflects actual computational cost, not unique events.
	IncErrorTotal(operatorName string)
	IncRetryTotal(operatorName string)
	IncDLQTotal(operatorName string)
	IncDLQOverflowTotal(operatorName string)
	IncDropTotal(operatorName string)
}

// noopErrorMetrics is a no-op implementation for use when no metrics
// system is configured.
type noopErrorMetrics struct{}

func (noopErrorMetrics) IncErrorTotal(_ string)       {}
func (noopErrorMetrics) IncRetryTotal(_ string)       {}
func (noopErrorMetrics) IncDLQTotal(_ string)         {}
func (noopErrorMetrics) IncDLQOverflowTotal(_ string) {}
func (noopErrorMetrics) IncDropTotal(_ string)        {}

// NoopErrorMetrics returns an ErrorMetrics that discards all observations.
func NoopErrorMetrics() ErrorMetrics {
	return noopErrorMetrics{}
}

// ClassifiedErrorMetrics optionally adds the bounded error class without
// breaking custom collectors implementing the original ErrorMetrics interface.
type ClassifiedErrorMetrics interface {
	IncClassifiedErrorTotal(operatorName string, class ErrorClass)
}

func recordOperatorError(metrics ErrorMetrics, operator string, class ErrorClass) {
	if classified, ok := metrics.(ClassifiedErrorMetrics); ok {
		classified.IncClassifiedErrorTotal(operator, class)
	} else if metrics != nil {
		metrics.IncErrorTotal(operator)
	}
}

type telemetryErrorMetrics struct {
	recorder *observability.OperatorErrorRecorder
}

// NewTelemetryErrorMetrics connects operator error handling to the configured
// OTel provider. NoopErrorMetrics remains an explicit opt-out.
func NewTelemetryErrorMetrics(taskID string) ErrorMetrics {
	return telemetryErrorMetrics{recorder: observability.NewOperatorErrorRecorder(taskID)}
}
func (m telemetryErrorMetrics) IncErrorTotal(op string) { m.recorder.Error(op, "unknown") }
func (m telemetryErrorMetrics) IncClassifiedErrorTotal(op string, class ErrorClass) {
	label := "unknown"
	switch class {
	case ErrorClassTransient:
		label = "transient"
	case ErrorClassPoison:
		label = "poison"
	case ErrorClassFatal:
		label = "fatal"
	}
	m.recorder.Error(op, label)
}
func (m telemetryErrorMetrics) IncRetryTotal(op string)       { m.recorder.Retry(op) }
func (m telemetryErrorMetrics) IncDLQTotal(op string)         { m.recorder.DLQ(op) }
func (m telemetryErrorMetrics) IncDLQOverflowTotal(op string) { m.recorder.Overflow(op) }
func (m telemetryErrorMetrics) IncDropTotal(op string)        { m.recorder.Drop(op) }

// Preserve the default path's original error and panic propagation while still
// observing failed calls. Cancellation is not an operator error.
func invokeLegacyWithMetrics(cc *chainContext, link ChainLink, fn func() error) error {
	defer func() {
		if r := recover(); r != nil {
			if cc.ctx.Err() == nil {
				recordOperatorError(cc.errMetrics, link.Config.OperatorName, ErrorClassPoison)
			}
			panic(r)
		}
	}()
	err := fn()
	if err != nil && cc.ctx.Err() == nil {
		recordOperatorError(cc.errMetrics, link.Config.OperatorName, defaultClassifier(err))
	}
	return err
}
