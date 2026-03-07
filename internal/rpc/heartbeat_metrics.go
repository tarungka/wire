package rpc

import "time"

// HeartbeatMetrics abstracts heartbeat-related metrics collection.
// Implementations may bridge to Prometheus or other systems.
type HeartbeatMetrics interface {
	ObserveLatency(d time.Duration)
	IncFailuresTotal()
	SetWorkersAlive(count int)
	IncWorkersLostTotal()
}

// noopHeartbeatMetrics is a no-op implementation for use when no metrics
// system is configured.
type noopHeartbeatMetrics struct{}

func (noopHeartbeatMetrics) ObserveLatency(_ time.Duration) {}
func (noopHeartbeatMetrics) IncFailuresTotal()              {}
func (noopHeartbeatMetrics) SetWorkersAlive(_ int)          {}
func (noopHeartbeatMetrics) IncWorkersLostTotal()           {}

// NoopHeartbeatMetrics returns a HeartbeatMetrics that discards all observations.
func NoopHeartbeatMetrics() HeartbeatMetrics {
	return noopHeartbeatMetrics{}
}
