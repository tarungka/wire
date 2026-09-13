package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ObserveTaskChannels registers scrape-time queue occupancy for a live task.
// The caller must unregister when the task exits to release both its labels
// and the closures retaining its channels. The read callback must be race-safe.
func ObserveTaskChannels(taskID string, read func() (int, int)) (func() error, error) {
	return observeTaskChannels(Meter(), taskID, read)
}

func observeTaskChannels(m metric.Meter, taskID string, read func() (int, int)) (func() error, error) {
	input, err := m.Int64ObservableGauge("wire_task_input_channel_usage", metric.WithDescription("Events queued at task input"))
	if err != nil {
		return nil, err
	}
	output, err := m.Int64ObservableGauge("wire_task_output_channel_usage", metric.WithDescription("Events queued at task output"))
	if err != nil {
		return nil, err
	}
	attrs := metric.WithAttributes(attribute.String("task_id", taskID))
	registration, err := m.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		in, out := read()
		observer.ObserveInt64(input, int64(in), attrs)
		observer.ObserveInt64(output, int64(out), attrs)
		return nil
	}, input, output)
	if err != nil {
		return nil, err
	}
	return registration.Unregister, nil
}
