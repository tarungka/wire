package engine

// ErrorMetrics abstracts error-handling metrics collection.
// Implementations may bridge to Prometheus or other systems.
type ErrorMetrics interface {
	IncErrorTotal(operatorName string)
	IncRetryTotal(operatorName string)
	IncDLQTotal(operatorName string)
	IncDropTotal(operatorName string)
}

// noopErrorMetrics is a no-op implementation for use when no metrics
// system is configured.
type noopErrorMetrics struct{}

func (noopErrorMetrics) IncErrorTotal(_ string) {}
func (noopErrorMetrics) IncRetryTotal(_ string) {}
func (noopErrorMetrics) IncDLQTotal(_ string)   {}
func (noopErrorMetrics) IncDropTotal(_ string)  {}

// NoopErrorMetrics returns an ErrorMetrics that discards all observations.
func NoopErrorMetrics() ErrorMetrics {
	return noopErrorMetrics{}
}
