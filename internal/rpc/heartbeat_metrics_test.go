package rpc

import (
	"testing"
	"time"
)

func TestNoopHeartbeatMetrics(t *testing.T) {
	m := NoopHeartbeatMetrics()

	// All methods should be callable without panic.
	m.ObserveLatency(100 * time.Millisecond)
	m.IncFailuresTotal()
	m.SetWorkersAlive(5)
	m.IncWorkersLostTotal()
}
