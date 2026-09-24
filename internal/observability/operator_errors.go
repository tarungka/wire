package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// OperatorErrorRecorder uses bounded error classes, never error messages, as
// labels. A retry failure counts another invocation error, not another record.
type OperatorErrorRecorder struct {
	errors, retries, dlq, overflow, drops metric.Int64Counter
	taskID                                string
}

func NewOperatorErrorRecorder(taskID string) *OperatorErrorRecorder {
	return newOperatorErrorRecorder(Meter(), taskID)
}

func newOperatorErrorRecorder(m metric.Meter, taskID string) *OperatorErrorRecorder {
	counter := func(name, description string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(description))
		if err != nil {
			return nil
		}
		return c
	}
	return &OperatorErrorRecorder{
		taskID:   taskID,
		errors:   counter("wire_operator_errors_total", "Failed operator invocations, including retries"),
		retries:  counter("wire_operator_retries_total", "Retried operator invocations"),
		dlq:      counter("wire_dlq_events_total", "Events delivered to a best-effort DLQ destination"),
		overflow: counter("wire_dlq_overflow_total", "DLQ events dropped because the channel is full"),
		drops:    counter("wire_operator_drops_total", "Events dropped by policy or failed DLQ delivery"),
	}
}

func (r *OperatorErrorRecorder) add(counter metric.Int64Counter, operator string, extra ...attribute.KeyValue) {
	if counter == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("operator", operator)}
	if r.taskID != "" {
		attrs = append(attrs, attribute.String("task_id", r.taskID))
	}
	attrs = append(attrs, extra...)
	counter.Add(context.Background(), 1, metric.WithAttributes(attrs...))
}
func (r *OperatorErrorRecorder) Error(operator, class string) {
	// Defensive cardinality bound for callers outside the engine.
	switch class {
	case "transient", "poison", "fatal":
	default:
		class = "unknown"
	}
	r.add(r.errors, operator, attribute.String("error_type", class))
}
func (r *OperatorErrorRecorder) Retry(operator string)    { r.add(r.retries, operator) }
func (r *OperatorErrorRecorder) DLQ(operator string)      { r.add(r.dlq, operator) }
func (r *OperatorErrorRecorder) Overflow(operator string) { r.add(r.overflow, operator) }
func (r *OperatorErrorRecorder) Drop(operator string)     { r.add(r.drops, operator) }
