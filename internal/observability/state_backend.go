package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// StateBackendRecorder observes logical HashMap payload bytes, not process RSS.
type StateBackendRecorder struct{ registration metric.Registration }

func NewStateBackendRecorder(operator, task string, usage func() int64) (*StateBackendRecorder, error) {
	return newStateBackendRecorder(Meter(), operator, task, usage)
}
func newStateBackendRecorder(meter metric.Meter, operator, task string, usage func() int64) (*StateBackendRecorder, error) {
	if operator == "" || task == "" || usage == nil {
		return nil, fmt.Errorf("state backend metrics require operator, task and usage")
	}
	gauge, err := meter.Int64ObservableGauge("wire_state_backend_memory_bytes", metric.WithUnit("By"), metric.WithDescription("Logical HashMap key and value bytes; excludes index, runtime, iterator and snapshot allocations"))
	if err != nil {
		return nil, err
	}
	attrs := []attribute.KeyValue{attribute.String("backend", "hashmap"), attribute.String("operator", operator), attribute.String("task_id", task)}
	registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		observer.ObserveInt64(gauge, usage(), metric.WithAttributes(attrs...))
		return nil
	}, gauge)
	if err != nil {
		return nil, err
	}
	return &StateBackendRecorder{registration: registration}, nil
}
func (r *StateBackendRecorder) Close() error {
	if r == nil || r.registration == nil {
		return nil
	}
	return r.registration.Unregister()
}
