package observability

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestStateBackendPrometheusMetricAndCleanup(t *testing.T) {
	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		t.Fatal(err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	recorder, err := newStateBackendRecorder(provider.Meter("wire"), "state", "task-1", func() int64 { return 42 })
	if err != nil {
		t.Fatal(err)
	}
	check := func(present bool) {
		t.Helper()
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, family := range families {
			if family.GetName() != "wire_state_backend_memory_bytes" {
				continue
			}
			found = true
			if len(family.Metric) != 1 || family.Metric[0].GetGauge().GetValue() != 42 {
				t.Fatalf("invalid gauge: %v", family)
			}
			labels := map[string]string{}
			for _, label := range family.Metric[0].Label {
				labels[label.GetName()] = label.GetValue()
			}
			for label, want := range map[string]string{"operator": "state", "task_id": "task-1", "backend": "hashmap"} {
				if labels[label] != want {
					t.Fatalf("incorrect labels %v", labels)
				}
			}
		}
		if found != present {
			t.Fatalf("metric present=%t want %t", found, present)
		}
	}
	check(true)
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	check(false)
}
