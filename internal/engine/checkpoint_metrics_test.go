package engine

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestDefaultEngineMetricsDoNotCountCoordinatorTimeouts(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	defer func() { otel.SetMeterProvider(previous); _ = provider.Shutdown(context.Background()) }()
	metrics := newTelemetryCheckpointMetrics("task")
	metrics.IncTimeoutTotal()
	metrics.ObserveAlignmentTime(time.Millisecond)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "wire_checkpoint_timeout_total" {
				t.Fatal("engine emitted a coordinator timeout counter")
			}
			if m.Name == "wire_checkpoint_alignment_time_ms" {
				h := m.Data.(metricdata.Histogram[float64])
				if len(h.DataPoints) != 1 {
					t.Fatal("missing alignment observation")
				}
				value, ok := h.DataPoints[0].Attributes.Value("task_id")
				if !ok || value.AsString() != "task" {
					t.Fatal("alignment lost task identity")
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatal("engine did not emit alignment telemetry")
	}
}

func TestNoopCheckpointMetrics(t *testing.T) {
	m := NoopCheckpointMetrics()
	// Verify no panics on all method calls.
	m.IncTimeoutTotal()
	m.ObserveAlignmentTime(100 * time.Millisecond)
}
