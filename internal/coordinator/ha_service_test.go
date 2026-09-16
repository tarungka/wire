package coordinator

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
)

func waitHATerm(t *testing.T, h *HAService) *haTerm {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if term := h.active.Load(); term != nil && term.coord.IsReady() {
			return term
		}
		select {
		case <-deadline.C:
			t.Fatal("coordinator did not become ready")
		case <-ticker.C:
		}
	}
}

func TestHAServiceSharedMetadataTakeover(t *testing.T) {
	dir := t.TempDir()
	open := func() (MetadataStore, error) { return NewPebbleStore(filepath.Join(dir, "metadata")) }
	firstElection := NewFileLockElection(filepath.Join(dir, "leader.lock"), "first:4001")
	secondElection := NewFileLockElection(filepath.Join(dir, "leader.lock"), "second:4001")
	defer firstElection.Close()
	defer secondElection.Close()
	first := NewHAService(CoordinatorConfig{NodeID: "first", ListenAddr: "first:4001"}, "", firstElection, open, nil, zerolog.Nop())
	second := NewHAService(CoordinatorConfig{NodeID: "second", ListenAddr: "second:4001"}, "", secondElection, open, nil, zerolog.Nop())
	ctx1, stop1 := context.WithCancel(context.Background())
	ctx2, stop2 := context.WithCancel(context.Background())
	defer stop1()
	defer stop2()
	done1, done2 := make(chan error, 1), make(chan error, 1)
	go func() { done1 <- first.campaign(ctx1) }()
	term1 := waitHATerm(t, first)
	job, err := term1.coord.SubmitJob("durable-job", 1, []byte("opaque"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { done2 <- second.campaign(ctx2) }()
	// Both nodes are live. The standby must not try to open the database held
	// by the elected leader; after release it recovers the same durable job.
	stop1()
	if err := <-done1; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	term2 := waitHATerm(t, second)
	if term2.coord.CurrentEpoch() <= term1.coord.CurrentEpoch() {
		t.Fatal("takeover reused fencing epoch")
	}
	recovered, err := term2.coord.GetJob(job.ID)
	if err != nil || recovered.Name != job.Name {
		t.Fatalf("lost acknowledged job on takeover: %+v %v", recovered, err)
	}
	if _, err := term1.coord.SubmitJob("zombie", 1, nil); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old term admitted mutation: %v", err)
	}
	if err := term1.coord.store.Set(JobMetaKey(job.ID), []byte("corrupt")); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old store revived: %v", err)
	}
	stop2()
	if err := <-done2; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type delayedRecoveryStore struct {
	MetadataStore
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (s *delayedRecoveryStore) PrefixScan(prefix []byte, fn func([]byte, []byte) bool) error {
	select {
	case <-s.entered:
	default:
		close(s.entered)
		<-s.release
	}
	return s.MetadataStore.PrefixScan(prefix, fn)
}
func (s *delayedRecoveryStore) Close() error { close(s.closed); return s.MetadataStore.Close() }

func TestHALossDuringRecoveryNeverPublishesReady(t *testing.T) {
	backend := &delayedRecoveryStore{MetadataStore: NewMemoryStore(), entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	h := NewHAService(CoordinatorConfig{NodeID: "node"}, "", nil, func() (MetadataStore, error) { return backend, nil }, nil, zerolog.Nop())
	ctx, revoke := context.WithCancel(context.Background())
	defer revoke()
	done := make(chan error, 1)
	go func() { done <- h.runTerm(context.Background(), &LeaderContext{Epoch: 42, Ctx: ctx, Cancel: revoke}) }()
	<-backend.entered
	revoke()
	close(backend.release)
	if err := <-done; err == nil {
		t.Fatal("recovery ignored revoked authority")
	}
	if h.active.Load() != nil {
		t.Fatal("published ready coordinator after election loss")
	}
	select {
	case <-backend.closed:
	default:
		t.Fatal("revoked recovery retained database ownership")
	}
}

func TestRecoveryWaitsForPreviousWorkerAuthority(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := slotReleaseJob(t, "recovering")
	job.Status = JobFailing
	job.LatestCheckpoint = 1
	c.jobs[job.ID] = job
	assignment := TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch - 1, Assignments: map[string]string{"task": "old-worker"}}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	c.workers["old-worker"] = &WorkerMeta{ID: "old-worker", Lost: true}
	c.recoveryFenceUntil = time.Now().Add(time.Hour)
	if c.prepareTaskRestart(job) {
		t.Fatal("restarted before old worker authority expired")
	}
	// Its old tasks have stopped before the current worker re-registers.
	c.workers["old-worker"].LastHeartbeat = time.Now()
	c.taskStatuses["task"] = rpc.TaskStatusFailed
	if !c.prepareTaskRestart(job) {
		t.Fatal("current registration did not release recovery wait")
	}
	c.workers["old-worker"].LastHeartbeat = time.Time{}
	c.recoveryFenceUntil = time.Now().Add(-time.Second)
	if !c.prepareTaskRestart(job) {
		t.Fatal("expired authority still blocked recovery")
	}
}
