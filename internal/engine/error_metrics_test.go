package engine

import "testing"

func TestNoopErrorMetrics_ImplementsInterface(t *testing.T) {
	var m = NoopErrorMetrics()
	// Should not panic.
	m.IncErrorTotal("op1")
	m.IncRetryTotal("op1")
	m.IncDLQTotal("op1")
	m.IncDropTotal("op1")
}
