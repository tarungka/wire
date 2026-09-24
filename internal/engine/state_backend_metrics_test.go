package engine

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestHashMapMemoryMetricTracksStateAndClose(t *testing.T) {
	ctx := context.Background()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	defer func() { otel.SetMeterProvider(previous); _ = provider.Shutdown(ctx) }()
	backend, err := NewStateBackend(StateBackendConfig{Type: StateBackendHashMap, HashMapMemLimit: 8, MetricTaskID: "task-1", MetricOperatorID: "state"})
	if err != nil {
		t.Fatal(err)
	}
	collect := func(want int64, present bool) {
		t.Helper()
		var data metricdata.ResourceMetrics
		if err := reader.Collect(ctx, &data); err != nil {
			t.Fatal(err)
		}
		seen := false
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name != "wire_state_backend_memory_bytes" {
					continue
				}
				for _, point := range metric.Data.(metricdata.Gauge[int64]).DataPoints {
					seen = true
					if point.Value != want {
						t.Fatalf("memory=%d want %d", point.Value, want)
					}
					for label, value := range map[string]string{"backend": "hashmap", "operator": "state", "task_id": "task-1"} {
						attr, ok := point.Attributes.Value(attribute.Key(label))
						if !ok || attr.AsString() != value {
							t.Fatalf("bad attribution %v", point.Attributes)
						}
					}
				}
			}
		}
		if seen != present {
			t.Fatalf("series present=%t want %t", seen, present)
		}
	}
	collect(0, true)
	if err := backend.Put([]byte("k"), []byte("abc")); err != nil {
		t.Fatal(err)
	}
	collect(4, true)
	snapshot, err := backend.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Put([]byte("k"), []byte("12345678")); err != ErrMemoryLimitExceeded {
		t.Fatal(err)
	}
	collect(4, true)
	if err := backend.Delete([]byte("k")); err != nil {
		t.Fatal(err)
	}
	collect(0, true)
	if err := backend.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	collect(4, true)
	if err := backend.(BatchedStateBackend).ApplyBatch([]StateMutation{{Key: []byte("k"), Value: []byte("abcde")}}); err != nil {
		t.Fatal(err)
	}
	collect(6, true)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	collect(0, false)
}
