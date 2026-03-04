package coordinator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/protocol"
)

func TestCoordinator_SingleNode_StartStop(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		HeartbeatFlushInterval: 50 * time.Millisecond,
	}, store, nil, zerolog.Nop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx)
	}()

	// Wait for leader state.
	time.Sleep(100 * time.Millisecond)
	if !c.IsLeader() {
		t.Fatal("expected leader state in single-node mode")
	}
	if !c.IsReady() {
		t.Fatal("expected ready in single-node mode")
	}
	if c.CurrentEpoch() == 0 {
		t.Fatal("epoch should be > 0")
	}

	cancel()
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatalf("Run error: %v", err)
	}
}

func TestCoordinator_State(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	if c.State() != StateStandby {
		t.Fatalf("initial state should be STANDBY, got %s", c.State())
	}
}

func TestCoordinator_WriteThrough_Job(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.mu.Unlock()

	job := &JobMeta{
		ID:          "job-1",
		Name:        "test-job",
		Status:      JobRunning,
		Parallelism: 4,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := c.persistJob(job); err != nil {
		t.Fatalf("persistJob: %v", err)
	}

	// Verify in-memory.
	c.mu.RLock()
	if _, ok := c.jobs["job-1"]; !ok {
		t.Fatal("job not in in-memory cache")
	}
	c.mu.RUnlock()

	// Verify in store.
	val, err := store.Get(JobMetaKey("job-1"))
	if err != nil {
		t.Fatal(err)
	}
	if val == nil {
		t.Fatal("job not persisted to store")
	}
}

func TestCoordinator_WriteThrough_Worker(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.mu.Unlock()

	worker := &WorkerMeta{
		ID:             "w1",
		Address:        "localhost:5001",
		TaskSlotsTotal: 4,
	}

	if err := c.persistWorker(worker); err != nil {
		t.Fatalf("persistWorker: %v", err)
	}

	// Verify in-memory.
	c.mu.RLock()
	if _, ok := c.workers["w1"]; !ok {
		t.Fatal("worker not in in-memory cache")
	}
	c.mu.RUnlock()

	// Verify in store.
	val, err := store.Get(WorkerMetaKey("w1"))
	if err != nil {
		t.Fatal(err)
	}
	if val == nil {
		t.Fatal("worker not persisted to store")
	}
}

func TestCoordinator_HeartbeatFlush(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		HeartbeatFlushInterval: 50 * time.Millisecond,
	}, store, nil, zerolog.Nop())

	ctx := t.Context()

	go c.Run(ctx)
	time.Sleep(50 * time.Millisecond) // Wait for leader

	// Add a worker.
	c.mu.Lock()
	c.workers["w1"] = &WorkerMeta{
		ID:             "w1",
		Address:        "localhost:5001",
		TaskSlotsTotal: 4,
		LastHeartbeat:  time.Now().UTC(),
	}
	c.mu.Unlock()

	// Wait for heartbeat flush.
	time.Sleep(200 * time.Millisecond)

	val, err := store.Get(WorkerMetaKey("w1"))
	if err != nil {
		t.Fatal(err)
	}
	if val == nil {
		t.Fatal("worker heartbeat not flushed to store")
	}
}

func TestCoordinator_ConcurrentPersistJob(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.mu.Unlock()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			job := &JobMeta{
				ID:   fmt.Sprintf("job-%d", id),
				Name: fmt.Sprintf("test-job-%d", id),
			}
			if err := c.persistJob(job); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent persistJob error: %v", err)
	}

	// Verify all jobs are in cache and store.
	c.mu.RLock()
	if len(c.jobs) != n {
		t.Fatalf("expected %d jobs in cache, got %d", n, len(c.jobs))
	}
	c.mu.RUnlock()

	for i := 0; i < n; i++ {
		key := JobMetaKey(fmt.Sprintf("job-%d", i))
		val, err := store.Get(key)
		if err != nil {
			t.Fatalf("store.Get(%s): %v", key, err)
		}
		if val == nil {
			t.Fatalf("job-%d not in store", i)
		}
	}
}

func TestCoordinator_ConcurrentPersistWorker(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.mu.Unlock()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			w := &WorkerMeta{
				ID:             fmt.Sprintf("w-%d", id),
				Address:        fmt.Sprintf("localhost:%d", 5000+id),
				TaskSlotsTotal: 4,
			}
			if err := c.persistWorker(w); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent persistWorker error: %v", err)
	}

	c.mu.RLock()
	if len(c.workers) != n {
		t.Fatalf("expected %d workers in cache, got %d", n, len(c.workers))
	}
	c.mu.RUnlock()
}

func TestCoordinator_FlushHeartbeats_NoClobberPersistWorker(t *testing.T) {
	// Verify that flushHeartbeats does not overwrite a concurrent persistWorker update.
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		HeartbeatFlushInterval: time.Hour,
	}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.recovered = true
	c.mu.Unlock()

	// Add initial worker.
	initial := &WorkerMeta{
		ID:             "w1",
		Address:        "localhost:5001",
		TaskSlotsTotal: 4,
		LastHeartbeat:  time.Now().UTC(),
	}
	if err := c.persistWorker(initial); err != nil {
		t.Fatal(err)
	}

	// Run concurrent flushHeartbeats and persistWorker updates.
	const iterations = 100
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			c.flushHeartbeats(context.Background())
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			w := &WorkerMeta{
				ID:                 "w1",
				Address:            "localhost:5001",
				TaskSlotsTotal:     4,
				TaskSlotsAvailable: i, // changes each iteration
				LastHeartbeat:      time.Now().UTC(),
			}
			c.persistWorker(w)
		}
	}()

	wg.Wait()

	// After all operations, cache and store must agree.
	c.mu.RLock()
	cached := c.workers["w1"]
	c.mu.RUnlock()

	storeData, _ := store.Get(WorkerMetaKey("w1"))
	var stored WorkerMeta
	protocol.DecodeMsgPack(storeData, &stored)

	if cached.TaskSlotsAvailable != stored.TaskSlotsAvailable {
		t.Fatalf("cache/store mismatch after concurrent flush+persist: cache=%d store=%d",
			cached.TaskSlotsAvailable, stored.TaskSlotsAvailable)
	}
}

func TestCoordinator_PersistConsistency(t *testing.T) {
	// Verify that after concurrent writes, the cache and store contain the
	// same final value for each key (no stale cache entries).
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.mu.Unlock()

	// Write the same job ID 20 times concurrently with different names.
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(ver int) {
			defer wg.Done()
			job := &JobMeta{
				ID:   "contended-job",
				Name: fmt.Sprintf("version-%d", ver),
			}
			c.persistJob(job)
		}(i)
	}
	wg.Wait()

	// The cached value and stored value must agree.
	c.mu.RLock()
	cached := c.jobs["contended-job"]
	c.mu.RUnlock()

	storeData, _ := store.Get(JobMetaKey("contended-job"))
	var stored JobMeta
	protocol.DecodeMsgPack(storeData, &stored)

	if cached.Name != stored.Name {
		t.Fatalf("cache/store mismatch: cache=%q store=%q", cached.Name, stored.Name)
	}
}
