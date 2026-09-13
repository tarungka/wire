package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func TaskBackpressureRecorder(taskID string) (func(time.Duration), error) {
	counter, err := Meter().Float64Counter("wire_task_backpressure_time_ms", metric.WithDescription("Cumulative operator chain wait for output capacity in milliseconds"))
	if err != nil {
		return nil, err
	}
	attrs := metric.WithAttributes(attribute.String("task_id", taskID))
	return func(duration time.Duration) {
		counter.Add(context.Background(), float64(duration)/float64(time.Millisecond), attrs)
	}, nil
}

// CheckpointUploadRecorder measures only replication I/O, including failures
// and cancellation, excluding snapshot capture and completion delivery.
func CheckpointUploadRecorder() (func(context.Context, string, time.Duration), error) {
	return checkpointUploadRecorder(Meter())
}

func checkpointUploadRecorder(m metric.Meter) (func(context.Context, string, time.Duration), error) {
	h, err := m.Float64Histogram("wire_task_checkpoint_upload_duration_ms",
		metric.WithDescription("Checkpoint replication duration in milliseconds"),
		metric.WithExplicitBucketBoundaries(1, 5, 10, 50, 100, 500, 1000, 5000, 10000, 60000, 600000))
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, taskID string, duration time.Duration) {
		h.Record(ctx, float64(duration)/float64(time.Millisecond), metric.WithAttributes(attribute.String("task_id", taskID)))
	}, nil
}

// ObserveTaskChannels registers scrape-time queue occupancy for a live task.
// The caller must unregister when the task exits to release both its labels
// and the closures retaining its channels. The read callback must be race-safe.
func ObserveTaskChannels(taskID string, read func() (int, int), alignmentBytes ...func() int64) (func() error, error) {
	return observeTaskChannels(Meter(), taskID, read, alignmentBytes...)
}

func observeTaskChannels(m metric.Meter, taskID string, read func() (int, int), alignmentBytes ...func() int64) (func() error, error) {
	input, err := m.Int64ObservableGauge("wire_task_input_channel_usage", metric.WithDescription("Events queued at task input"))
	if err != nil {
		return nil, err
	}
	output, err := m.Int64ObservableGauge("wire_task_output_channel_usage", metric.WithDescription("Events queued at task output"))
	if err != nil {
		return nil, err
	}
	attrs := metric.WithAttributes(attribute.String("task_id", taskID))
	instruments := []metric.Observable{input, output}
	var alignment metric.Int64ObservableGauge
	if len(alignmentBytes) > 0 {
		alignment, err = m.Int64ObservableGauge("wire_task_alignment_buffer_bytes", metric.WithDescription("Logical event payload bytes owned by alignment buffers"))
		if err != nil {
			return nil, err
		}
		instruments = append(instruments, alignment)
	}
	registration, err := m.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		in, out := read()
		observer.ObserveInt64(input, int64(in), attrs)
		observer.ObserveInt64(output, int64(out), attrs)
		if alignment != nil {
			observer.ObserveInt64(alignment, alignmentBytes[0](), attrs)
		}
		return nil
	}, instruments...)
	if err != nil {
		return nil, err
	}
	return registration.Unregister, nil
}
