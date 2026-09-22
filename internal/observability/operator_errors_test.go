package observability

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestOperatorErrorMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())
	recorder := newOperatorErrorRecorder(provider.Meter("test"), "task-7")
	recorder.Error("parse", "poison")
	recorder.Error("parse", "poison")
	recorder.Error("sink", "transient")
	recorder.Retry("sink")
	recorder.DLQ("parse")
	recorder.Overflow("parse")
	recorder.Drop("parse")
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			for _, point := range metric.Data.(metricdata.Sum[int64]).DataPoints {
				task, _ := point.Attributes.Value("task_id")
				op, _ := point.Attributes.Value("operator")
				if task.AsString() != "task-7" || op.AsString() == "" {
					t.Fatalf("missing attribution: %+v", point)
				}
				if metric.Name == "wire_operator_errors_total" {
					class, _ := point.Attributes.Value("error_type")
					if op.AsString() == "parse" && (class.AsString() != "poison" || point.Value != 2) {
						t.Fatalf("wrong poison series: %+v", point)
					}
					if op.AsString() == "sink" && (class.AsString() != "transient" || point.Value != 1) {
						t.Fatalf("wrong transient series: %+v", point)
					}
				}
				seen[metric.Name] += point.Value
			}
		}
	}
	for name, want := range map[string]int64{"wire_operator_errors_total": 3, "wire_operator_retries_total": 1, "wire_dlq_events_total": 1, "wire_dlq_overflow_total": 1, "wire_operator_drops_total": 1} {
		if seen[name] != want {
			t.Errorf("%s=%d, want %d", name, seen[name], want)
		}
	}
}
