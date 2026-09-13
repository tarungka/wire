package observability

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestTaskChannelMetricsLifecycle(t *testing.T) {
	ctx := context.Background()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = provider.Shutdown(ctx) }()
	input, output := make(chan int, 3), make(chan int, 2)
	unregister, err := observeTaskChannels(provider.Meter("test"), "task-1", func() (int, int) { return len(input), len(output) })
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
	check(map[string]int64{"wire_task_input_channel_usage": 2, "wire_task_output_channel_usage": 1})
	<-input
	<-output
	check(map[string]int64{"wire_task_input_channel_usage": 1, "wire_task_output_channel_usage": 0})
	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	check(map[string]int64{})
}
