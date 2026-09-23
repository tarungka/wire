package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestPeriodicCheckpointTimingAndRunner(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	job := c.jobs["job"]
	now := time.Now()
	job.RunningSince = now
	job.CheckpointPolicy = &rpc.CheckpointPolicy{Interval: time.Second, Timeout: time.Minute, MinPause: 2 * time.Second}
	if len(c.duePeriodicCheckpoints(now.Add(time.Second-time.Nanosecond))) != 0 {
		t.Fatal("early trigger")
	}
	if len(c.duePeriodicCheckpoints(now.Add(time.Second))) != 1 {
		t.Fatal("interval ignored")
	}
	job.LastCheckpointCompletion = now
	if len(c.duePeriodicCheckpoints(now.Add(time.Second))) != 0 {
		t.Fatal("pause ignored")
	}
	if len(c.duePeriodicCheckpoints(now.Add(2*time.Second))) != 1 {
		t.Fatal("pause never expires")
	}
	job.LastCheckpointCompletion = time.Time{}
	job.RunningSince = now.Add(-time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.runPeriodicCheckpoints(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		c.mu.RLock()
		cp, active := c.activeCheckpoints["job"]
		c.mu.RUnlock()
		if active {
			cancel()
			<-done
			if len(c.duePeriodicCheckpoints(time.Now().Add(time.Hour))) != 0 {
				t.Fatal("concurrent checkpoint scheduled")
			}
			raw, err := store.Get(JobMetaKey("job"))
			if err != nil {
				t.Fatal(err)
			}
			var restored JobMeta
			if err := protocol.DecodeMsgPack(raw, &restored); err != nil {
				t.Fatal(err)
			}
			if !restored.LastCheckpointTrigger.Equal(cp.Timestamp) {
				t.Fatal("trigger time not persisted atomically")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("runner did not trigger checkpoint")
		case <-ticker.C:
		}
	}
}

func TestJobCheckpointPolicyOverridesCoordinator(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	c.config.CheckpointTimeout = time.Hour
	c.config.CheckpointMinPause = time.Hour
	job := c.jobs["job"]
	job.CheckpointPolicy = &rpc.CheckpointPolicy{Timeout: time.Second}
	job.LastCheckpointCompletion = time.Now()
	cp, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	c.expireCheckpoints(cp.Timestamp.Add(time.Second - time.Nanosecond))
	if len(c.activeCheckpoints) != 1 {
		t.Fatal("expired before job timeout")
	}
	c.expireCheckpoints(cp.Timestamp.Add(time.Second))
	if len(c.activeCheckpoints) != 0 {
		t.Fatal("job timeout ignored")
	}
	job.CheckpointPolicy.MinPause = time.Hour
	if _, err := c.TriggerCheckpoint("job"); !errors.Is(err, ErrCheckpointMinPause) {
		t.Fatalf("minimum pause: %v", err)
	}
	if _, err := c.triggerCheckpoint("job", "explicit-savepoint"); err != nil {
		t.Fatalf("savepoint paced: %v", err)
	}
}

func TestSubmittedCheckpointPolicyPersistsAndRejectsInvalid(t *testing.T) {
	c, store := newTestCoordinator(t)
	graph := rpc.JobGraph{CheckpointPolicy: &rpc.CheckpointPolicy{Interval: time.Second, Timeout: time.Minute, MinPause: 2 * time.Second}}
	raw, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		t.Fatal(err)
	}
	job, err := c.SubmitJob("policy", 1, raw)
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Get(JobMetaKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	var restored JobMeta
	if err := protocol.DecodeMsgPack(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.CheckpointPolicy == nil || *restored.CheckpointPolicy != *graph.CheckpointPolicy {
		t.Fatalf("policy lost: %+v", restored.CheckpointPolicy)
	}
	graph.CheckpointPolicy.Timeout = 0
	raw, err = protocol.EncodeMsgPack(graph)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SubmitJob("invalid", 1, raw); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid policy accepted: %v", err)
	}
}
