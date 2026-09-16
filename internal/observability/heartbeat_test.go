package observability

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestHeartbeatInstruments(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())
	meter := provider.Meter("heartbeat-test")
	h := HeartbeatMetrics{meter: meter}
	h.ObserveLatency(25 * time.Millisecond)
	h.IncFailuresTotal()
	h.IncWorkersLostTotal()
	alive := 2
	reg, err := registerWorkersAliveGauge(meter, func() int { return alive })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	check := func(wantAlive int64) {
		t.Helper()
		var data metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &data); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, scope := range data.ScopeMetrics {
			for _, m := range scope.Metrics {
				switch m.Name {
				case "wire_heartbeat_latency_ms":
					hist := m.Data.(metricdata.Histogram[float64])
					seen[m.Name] = len(hist.DataPoints) == 1 && hist.DataPoints[0].Sum == 25 && hist.DataPoints[0].Count == 1
				case "wire_heartbeat_failures_total", "wire_workers_lost_total":
					sum := m.Data.(metricdata.Sum[int64])
					seen[m.Name] = len(sum.DataPoints) == 1 && sum.DataPoints[0].Value == 1
				case "wire_workers_alive":
					g := m.Data.(metricdata.Gauge[int64])
					seen[m.Name] = len(g.DataPoints) == 1 && g.DataPoints[0].Value == wantAlive
				}
			}
		}
		for _, name := range []string{"wire_heartbeat_latency_ms", "wire_heartbeat_failures_total", "wire_workers_lost_total", "wire_workers_alive"} {
			if !seen[name] {
				t.Errorf("missing or incorrect metric %s", name)
			}
		}
	}
	check(2)
	alive = 0
	check(0)
}
