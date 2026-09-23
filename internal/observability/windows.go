package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// WindowRecorder counts arrivals during this execution; checkpoint restoration
// does not replay historical counters. Retention is a live logical-byte gauge.
type WindowRecorder struct {
	late, allowed, dropped metric.Int64Counter
	attrs                  []attribute.KeyValue
	registration           metric.Registration
}

func NewWindowRecorder(operator, task string, retention func() int64) (*WindowRecorder, error) {
	return newWindowRecorder(Meter(), operator, task, retention)
}
func newWindowRecorder(m metric.Meter, operator, task string, retention func() int64) (*WindowRecorder, error) {
	r := &WindowRecorder{attrs: []attribute.KeyValue{attribute.String("operator", operator)}}
	if task != "" {
		r.attrs = append(r.attrs, attribute.String("task_id", task))
	}
	var err error
	if r.late, err = m.Int64Counter("wire_late_events_total", metric.WithDescription("Events older than the operator watermark")); err != nil {
		return nil, err
	}
	if r.allowed, err = m.Int64Counter("wire_late_events_allowed_total", metric.WithDescription("Late events accepted by at least one nonexpired window")); err != nil {
		return nil, err
	}
	if r.dropped, err = m.Int64Counter("wire_late_events_dropped_total", metric.WithDescription("Events rejected by every assigned window, including routed late output")); err != nil {
		return nil, err
	}
	gauge, err := m.Int64ObservableGauge("wire_window_state_retention_bytes", metric.WithUnit("By"), metric.WithDescription("Logical key and accumulator bytes in closed but retained windows"))
	if err != nil {
		return nil, err
	}
	r.registration, err = m.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		observer.ObserveInt64(gauge, retention(), metric.WithAttributes(r.attrs...))
		return nil
	}, gauge)
	if err != nil {
		return nil, err
	}
	return r, nil
}
func (r *WindowRecorder) Record(ctx context.Context, late, allowed, dropped uint64) {
	if r == nil {
		return
	}
	for _, point := range []struct {
		counter metric.Int64Counter
		value   uint64
	}{{r.late, late}, {r.allowed, allowed}, {r.dropped, dropped}} {
		if point.value > 0 {
			point.counter.Add(ctx, int64(point.value), metric.WithAttributes(r.attrs...))
		}
	}
}
func (r *WindowRecorder) Close() error {
	if r == nil || r.registration == nil {
		return nil
	}
	return r.registration.Unregister()
}
