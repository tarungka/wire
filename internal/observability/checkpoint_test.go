package observability

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestCheckpointTimeoutAndAlignmentMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())
	meter := provider.Meter("checkpoint-test")
	recordCheckpointTimeout(meter, "job")
	checkpointAlignmentRecorder(meter, "task")(25 * time.Millisecond)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	var timeout, alignment bool
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch m.Name {
			case "wire_checkpoint_timeout_total":
				sum := m.Data.(metricdata.Sum[int64])
				timeout = len(sum.DataPoints) == 1 && sum.DataPoints[0].Value == 1
			case "wire_checkpoint_alignment_time_ms":
				h := m.Data.(metricdata.Histogram[float64])
				alignment = len(h.DataPoints) == 1 && h.DataPoints[0].Count == 1 && h.DataPoints[0].Sum == 25
			}
		}
	}
	if !timeout || !alignment {
		t.Fatalf("missing metrics timeout=%v alignment=%v", timeout, alignment)
	}
}
