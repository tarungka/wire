//go:build integration

package coordinator

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/protocol"
)

func TestIntegration_CrashRecovery(t *testing.T) {
	dir := t.TempDir()

	// --- Phase 1: First coordinator writes state, then "crashes" ---

	store1, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	c1 := New(CoordinatorConfig{
		NodeID:                 "n1",
		DataDir:                dir,
		ListenAddr:             ":4001",
		HeartbeatFlushInterval: 50 * time.Millisecond,
	}, store1, nil, zerolog.Nop())

	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() { done1 <- c1.Run(ctx1) }()

	time.Sleep(100 * time.Millisecond)
	if !c1.IsReady() {
		t.Fatal("c1 should be ready")
	}
	epoch1 := c1.CurrentEpoch()

	// Persist sample jobs.
	jobs := []*JobMeta{
		{ID: "j1", Name: "etl-pipeline", Status: JobRunning, Parallelism: 4, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{ID: "j2", Name: "analytics", Status: JobFinished, Parallelism: 2, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{ID: "j3", Name: "stream-join", Status: JobFailed, Parallelism: 8, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
	}
	for _, j := range jobs {
		if err := c1.persistJob(j); err != nil {
			t.Fatalf("persistJob %s: %v", j.ID, err)
		}
	}

	// Persist workers.
	workers := []*WorkerMeta{
		{ID: "w1", Address: "host1:5001", TaskSlotsTotal: 4, LastHeartbeat: time.Now().UTC()},
		{ID: "w2", Address: "host2:5001", TaskSlotsTotal: 8, LastHeartbeat: time.Now().UTC()},
	}
	for _, w := range workers {
		if err := c1.persistWorker(w); err != nil {
			t.Fatalf("persistWorker %s: %v", w.ID, err)
		}
	}

	// Persist checkpoints (one completed, one in-flight).
	cpCompleted := &CheckpointMeta{
		ID: 1, JobID: "j1", Status: CheckpointCompleted, Timestamp: time.Now().UTC(),
	}
	cpData, _ := protocol.EncodeMsgPack(cpCompleted)
	store1.Set(CheckpointKey("j1", 1), cpData)

	cpInFlight := &CheckpointMeta{
		ID: 2, JobID: "j1", Status: CheckpointTriggered, Timestamp: time.Now().UTC(),
	}
	cpData2, _ := protocol.EncodeMsgPack(cpInFlight)
	store1.Set(CheckpointKey("j1", 2), cpData2)

	// Wait for heartbeat flush.
	time.Sleep(200 * time.Millisecond)

	// "Crash" the coordinator.
	cancel1()
	<-done1
	store1.Close()

	// --- Phase 2: New coordinator recovers from same data dir ---

	store2, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store2.Close()

	c2 := New(CoordinatorConfig{
		NodeID:                 "n2",
		DataDir:                dir,
		ListenAddr:             ":4001",
		HeartbeatFlushInterval: 50 * time.Millisecond,
	}, store2, nil, zerolog.Nop())

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done2 := make(chan error, 1)
	go func() { done2 <- c2.Run(ctx2) }()

	time.Sleep(100 * time.Millisecond)
	if !c2.IsReady() {
		t.Fatal("c2 should be ready after recovery")
	}

	// Verify epoch incremented.
	epoch2 := c2.CurrentEpoch()
	if epoch2 <= epoch1 {
		t.Fatalf("epoch should increase: %d <= %d", epoch2, epoch1)
	}

	// Verify all jobs recovered.
	c2.mu.RLock()
	if len(c2.jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(c2.jobs))
	}
	if c2.jobs["j1"].Status != JobRunning {
		t.Fatalf("j1 status: expected RUNNING, got %s", c2.jobs["j1"].Status)
	}
	if c2.jobs["j2"].Status != JobFinished {
		t.Fatalf("j2 status: expected FINISHED, got %s", c2.jobs["j2"].Status)
	}
	c2.mu.RUnlock()

	// Verify workers recovered.
	c2.mu.RLock()
	if len(c2.workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(c2.workers))
	}
	c2.mu.RUnlock()

	// --- Phase 3: Worker re-registration with reconciliation ---

	// Persist task assignments for w1.
	assignedTasks := []string{"task-1", "task-2"}
	taskData, _ := protocol.EncodeMsgPack(assignedTasks)
	store2.Set(WorkerTasksKey("w1"), taskData)

	// Worker reports task-1 (match) and task-3 (orphan). task-2 is missing.
	resp, err := c2.RegisterWorker(RegisterWorkerRequest{
		WorkerID:         "w1",
		Address:          "host1:5001",
		TaskSlotsTotal:   4,
		HighestSeenEpoch: epoch2,
		RunningTasks:     []string{"task-1", "task-3"},
	})
	if err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}
	if resp.Epoch != epoch2 {
		t.Fatalf("expected epoch %d, got %d", epoch2, resp.Epoch)
	}
	// task-3 is orphaned and should be canceled.
	if len(resp.TasksToCancel) != 1 || resp.TasksToCancel[0] != "task-3" {
		t.Fatalf("expected [task-3] to cancel, got %v", resp.TasksToCancel)
	}

	cancel2()
	<-done2
}

func TestIntegration_HTTPEndpoints(t *testing.T) {
	dir := t.TempDir()

	store, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		DataDir:                dir,
		ListenAddr:             ":0",
		HeartbeatFlushInterval: time.Hour,
	}, store, nil, zerolog.Nop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	time.Sleep(100 * time.Millisecond)

	srv := NewHTTPServer(c, "127.0.0.1:0", zerolog.Nop())
	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go srv.Serve()
	defer srv.Shutdown(context.Background())

	// Smoke test all endpoints.
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/cluster/leader"} {
		resp, err := http.Get(fmt.Sprintf("http://%s%s", srv.Addr(), path))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			t.Fatalf("GET %s: unexpected status %d", path, resp.StatusCode)
		}
	}
}
