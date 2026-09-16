package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// HeartbeatMetrics implements the heartbeat instrumentation contract without
// importing the RPC package. Instruments resolve against the current provider.
type HeartbeatMetrics struct{ meter metric.Meter }

func (h HeartbeatMetrics) instrumentMeter() metric.Meter {
	if h.meter != nil {
		return h.meter
	}
	return Meter()
}

func (h HeartbeatMetrics) ObserveLatency(d time.Duration) {
	histogram, err := h.instrumentMeter().Float64Histogram("wire_heartbeat_latency_ms", metric.WithDescription("Heartbeat round-trip latency in milliseconds"), metric.WithExplicitBucketBoundaries(1, 5, 10, 50, 100, 500, 1000, 5000))
	if err == nil {
		histogram.Record(context.Background(), float64(d)/float64(time.Millisecond))
	}
}
func (h HeartbeatMetrics) IncFailuresTotal() {
	c, err := h.instrumentMeter().Int64Counter("wire_heartbeat_failures_total")
	if err == nil {
		c.Add(context.Background(), 1)
	}
}
func (h HeartbeatMetrics) IncWorkersLostTotal() {
	c, err := h.instrumentMeter().Int64Counter("wire_workers_lost_total")
	if err == nil {
		c.Add(context.Background(), 1)
	}
}
func (h HeartbeatMetrics) SetWorkersAlive(count int) {
	g, err := h.instrumentMeter().Int64Gauge("wire_workers_alive")
	if err == nil {
		g.Record(context.Background(), int64(count))
	}
}

func RegisterWorkersAliveGauge(count func() int) (metric.Registration, error) {
	return registerWorkersAliveGauge(Meter(), count)
}
func registerWorkersAliveGauge(m metric.Meter, count func() int) (metric.Registration, error) {
	g, err := m.Int64ObservableGauge("wire_workers_alive")
	if err != nil {
		return nil, err
	}
	return m.RegisterCallback(func(_ context.Context, o metric.Observer) error { o.ObserveInt64(g, int64(count())); return nil }, g)
}
