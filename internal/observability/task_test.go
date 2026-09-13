package observability

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestTaskChannelMetricsLifecycle(t *testing.T) {
	ctx := context.Background()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = provider.Shutdown(ctx) }()
	input, output := make(chan int, 3), make(chan int, 2)
	unregister, err := observeTaskChannels(provider.Meter("test"), "task-1", func() (int, int) { return len(input), len(output) }, func() int64 { return int64(len(input) * 19) })
	if err != nil {
		t.Fatal(err)
	}
	input <- 1
	input <- 2
	output <- 1
	check := func(want map[string]int64) {
		t.Helper()
		var data metricdata.ResourceMetrics
		if err := reader.Collect(ctx, &data); err != nil {
			t.Fatal(err)
		}
		found := make(map[string]int64)
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				gauge, ok := metric.Data.(metricdata.Gauge[int64])
				if !ok {
					t.Fatalf("unexpected metric type %T", metric.Data)
				}
				for _, point := range gauge.DataPoints {
					id, ok := point.Attributes.Value("task_id")
					if !ok || id.AsString() != "task-1" {
						t.Fatalf("task identity: %v", point.Attributes)
					}
					found[metric.Name] = point.Value
				}
			}
		}
		if len(found) != len(want) {
			t.Fatalf("got %v want %v", found, want)
		}
		for name, value := range want {
			if got, ok := found[name]; !ok || got != value {
				t.Fatalf("%s: got %d want %d", name, got, value)
			}
		}
	}
	check(map[string]int64{"wire_task_input_channel_usage": 2, "wire_task_output_channel_usage": 1, "wire_task_alignment_buffer_bytes": 38})
	<-input
	<-output
	check(map[string]int64{"wire_task_input_channel_usage": 1, "wire_task_output_channel_usage": 0, "wire_task_alignment_buffer_bytes": 19})
	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	check(map[string]int64{})
}

func TestCheckpointUploadMetricUnits(t *testing.T) {
	ctx := context.Background()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = provider.Shutdown(ctx) }()
	record, err := checkpointUploadRecorder(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	record(ctx, "task", 1500*time.Microsecond)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	record(canceled, "task", 2*time.Millisecond)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &data); err != nil {
		t.Fatal(err)
	}
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "wire_task_checkpoint_upload_duration_ms" {
				continue
			}
			h := m.Data.(metricdata.Histogram[float64])
			if len(h.DataPoints) != 1 {
				t.Fatalf("points: %v", h.DataPoints)
			}
			p := h.DataPoints[0]
			if p.Count != 2 || p.Sum != 3.5 {
				t.Fatalf("milliseconds: count=%d sum=%f", p.Count, p.Sum)
			}
			if len(p.Bounds) == 0 || p.Bounds[len(p.Bounds)-1] != 600000 {
				t.Fatalf("bounds: %v", p.Bounds)
			}
			return
		}
	}
	t.Fatal("upload metric missing")
}
