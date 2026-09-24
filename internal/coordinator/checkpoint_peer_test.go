package coordinator

import (
	"testing"
	"time"
)

func TestCheckpointPeerExcludesSelfAndExpiredWorkers(t *testing.T) {
	c, _ := newTestCoordinator(t)
	now := time.Now()
	c.workers["source"] = &WorkerMeta{CheckpointAddress: "source:1", LastHeartbeat: now}
	c.workers["a"] = &WorkerMeta{CheckpointAddress: "expired:1", LastHeartbeat: now.Add(-c.config.WorkerTimeout)}
	c.workers["b"] = &WorkerMeta{CheckpointAddress: "source:1", LastHeartbeat: now}
	c.workers["d"] = &WorkerMeta{CheckpointAddress: "second:1", LastHeartbeat: now}
	c.workers["c"] = &WorkerMeta{CheckpointAddress: "first:1", LastHeartbeat: now}
	if got := c.checkpointPeerLocked("source", now); got != "first:1" {
		t.Fatalf("selected %q", got)
	}
	delete(c.workers, "c")
	delete(c.workers, "d")
	if got := c.checkpointPeerLocked("source", now); got != "" {
		t.Fatalf("selected unavailable peer %q", got)
	}
}
