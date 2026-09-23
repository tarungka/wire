package observability

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestWindowMetricsAttributionRetentionAndClose(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())
	retained := int64(12)
	r, err := newWindowRecorder(provider.Meter("test"), "window", "task-1", func() int64 { return retained })
	if err != nil {
		t.Fatal(err)
	}
	r.Record(context.Background(), 2, 1, 1)
	collect := func(wantGauge bool) {
		t.Helper()
		var data metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &data); err != nil {
			t.Fatal(err)
		}
		seen := map[string]int64{}
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				switch points := metric.Data.(type) {
				case metricdata.Sum[int64]:
					for _, point := range points.DataPoints {
						task, _ := point.Attributes.Value("task_id")
						op, _ := point.Attributes.Value("operator")
						if task.AsString() != "task-1" || op.AsString() != "window" {
							t.Fatalf("bad attributes %v", point.Attributes)
						}
						seen[metric.Name] += point.Value
					}
				case metricdata.Gauge[int64]:
					for _, point := range points.DataPoints {
						seen[metric.Name] = point.Value
					}
				}
			}
		}
		for name, want := range map[string]int64{"wire_late_events_total": 2, "wire_late_events_allowed_total": 1, "wire_late_events_dropped_total": 1} {
			if seen[name] != want {
				t.Fatalf("%s=%d", name, seen[name])
			}
		}
		got, exists := seen["wire_window_state_retention_bytes"]
		if exists != wantGauge || wantGauge && got != retained {
			t.Fatalf("gauge=%d exists=%t", got, exists)
		}
	}
	collect(true)
	retained = 0
	collect(true)
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	collect(false)
}
