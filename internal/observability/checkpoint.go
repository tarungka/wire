package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func RecordCheckpointTimeout(jobID string) { recordCheckpointTimeout(Meter(), jobID) }

func recordCheckpointTimeout(m metric.Meter, jobID string) {
	counter, err := m.Int64Counter("wire_checkpoint_timeout_total", metric.WithDescription("Checkpoint decisions aborted due to timeout"))
	if err == nil {
		counter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("job_id", jobID)))
	}
}

func CheckpointAlignmentRecorder(taskID string) func(time.Duration) {
	return checkpointAlignmentRecorder(Meter(), taskID)
}

func checkpointAlignmentRecorder(m metric.Meter, taskID string) func(time.Duration) {
	histogram, err := m.Float64Histogram("wire_checkpoint_alignment_time_ms", metric.WithDescription("Barrier alignment duration, including aborted alignments"), metric.WithExplicitBucketBoundaries(1, 10, 100, 1000, 10000, 60000, 600000))
	if err != nil {
		return func(time.Duration) {}
	}
	return func(duration time.Duration) {
		histogram.Record(context.Background(), float64(duration)/float64(time.Millisecond), metric.WithAttributes(attribute.String("task_id", taskID)))
	}
}
