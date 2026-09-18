package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

type processTransactionConfig struct{ WorkerID, Coordinator, Root, Ledger string }
type gatedTransactionSource struct {
	next int
	root string
}

func (*gatedTransactionSource) Open(context.Context) error          { return nil }
func (*gatedTransactionSource) Close() error                        { return nil }
func (*gatedTransactionSource) GenerateWatermark() int64            { return 0 }
func (s *gatedTransactionSource) Checkpoint(uint64) ([]byte, error) { return json.Marshal(s.next) }
func (s *gatedTransactionSource) RestoreCheckpoint(data []byte) error {
	return json.Unmarshal(data, &s.next)
}
func (s *gatedTransactionSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	data, err := os.ReadFile(filepath.Join(s.root, "limit"))
	if err != nil {
		return nil, err
	}
	var limit int
	if err = json.Unmarshal(data, &limit); err != nil {
		return nil, err
	}
	if s.next == 1000 && limit > 1000 {
		return nil, nil
	}
	if s.next >= min(limit, 1000) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
			return []engine.Event{}, nil
		}
	}
	events := make([]engine.Event, min(10, min(limit, 1000)-s.next))
	for i := range events {
		events[i] = engine.Event{Value: []byte(fmt.Sprint(s.next))}
		s.next++
	}
	return events, nil
}

// Invoked as a separate OS process by the acceptance test. Killing it bypasses
// every Go defer, task cancellation callback, and sink cleanup method.
func TestTransactionalWorkerProcess(t *testing.T) {
	raw := os.Getenv("WIRE_TRANSACTION_PROCESS")
	if raw == "" {
		t.Skip("subprocess helper")
	}
	var cfg processTransactionConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	registry := worker.NewRegistry()
	registry.RegisterSource("gated", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
		return &gatedTransactionSource{root: cfg.Ledger}, nil
	})
	ledger := &durableSinkLedger{root: cfg.Ledger}
	registry.RegisterSink("durable", func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.SinkOperator, error) {
		sink := &durableClusterSink{ledger: ledger, task: tc.TaskID}
		sink.afterPrepare = func(ctx context.Context, id uint64) error {
			if id != 3 {
				return nil
			}
			if _, err := os.Stat(filepath.Join(cfg.Ledger, "pause-third")); os.IsNotExist(err) {
				return nil
			}
			if err := os.WriteFile(filepath.Join(cfg.Ledger, "prepared-third"), []byte(tc.TaskID), 0600); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}
		return sink, nil
	})
	for _, dir := range []string{"replicas", "artifacts", "staging"} {
		if err := os.MkdirAll(filepath.Join(cfg.Root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	replica := &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: filepath.Join(cfg.Root, "replicas"), ArtifactRoot: filepath.Join(cfg.Root, "artifacts"), StagingRoot: filepath.Join(cfg.Root, "staging"), Concurrency: 1}
	w := worker.NewWithRegistry(worker.Config{WorkerID: cfg.WorkerID, CoordinatorAddr: cfg.Coordinator, TaskSlots: 1, CheckpointReplica: replica, HeartbeatInterval: 50 * time.Millisecond, HeartbeatTimeout: 500 * time.Millisecond}, registry, zerolog.Nop())
	if err := w.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionalRecoveryAfterProcessKillDuringThirdCheckpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	store := coordinator.NewMemoryStore()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "coordinator", WorkerTimeout: 500 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond, RestartBackoff: 20 * time.Millisecond}, store, nil, zerolog.Nop())
	coordDone := make(chan error, 1)
	go func() { coordDone <- coord.Run(ctx) }()
	waitFor(t, 2*time.Second, coord.IsReady)
	server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(ctx) }()
	defer func() { cancel(); _ = server.Shutdown(context.Background()); <-serverDone; <-coordDone }()
	ledger := t.TempDir()
	writeLimit := func(limit int) {
		t.Helper()
		data, _ := json.Marshal(limit)
		path := filepath.Join(ledger, "limit")
		if err := os.WriteFile(path+".next", data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".next", path); err != nil {
			t.Fatal(err)
		}
	}
	writeLimit(300)
	if err := os.WriteFile(filepath.Join(ledger, "pause-third"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	configs := map[string]processTransactionConfig{}
	processes := map[string]*exec.Cmd{}
	start := func(id string) {
		t.Helper()
		cfg := configs[id]
		data, _ := json.Marshal(cfg)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTransactionalWorkerProcess$", "-test.timeout=45s")
		cmd.Env = append(os.Environ(), "WIRE_TRANSACTION_PROCESS="+string(data))
		log, err := os.CreateTemp(t.TempDir(), "worker-*.log")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if t.Failed() {
				data, _ := os.ReadFile(log.Name())
				t.Logf("worker %s: %s", id, data)
			}
			_ = log.Close()
		})
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		processes[id] = cmd
	}
	defer func() {
		for _, cmd := range processes {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	for _, id := range []string{"one", "two"} {
		configs[id] = processTransactionConfig{WorkerID: id, Coordinator: server.Addr(), Root: t.TempDir(), Ledger: ledger}
		start(id)
	}
	waitFor(t, 5*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
	graph, err := protocol.EncodeMsgPack(rpc.JobGraph{Operators: []rpc.OperatorDescriptor{{OperatorID: "source", Type: rpc.OperatorTypeSource, ClassName: "gated"}, {OperatorID: "sink", Type: rpc.OperatorTypeSink, ClassName: "durable"}}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyForward}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := coord.SubmitJob("transaction-crash", 1, graph)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		job, err := coord.GetJob(job.ID)
		return err == nil && job.Status == coordinator.JobRunning
	})
	data, err := store.Get(coordinator.JobAssignmentsKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	var assignment coordinator.TaskAssignmentMap
	if err := protocol.DecodeMsgPack(data, &assignment); err != nil {
		t.Fatal(err)
	}
	var taskID, owner string
	for taskID, owner = range assignment.Assignments {
		break
	}
	path := filepath.Join(ledger, fmt.Sprintf("%x.json", taskID))
	readState := func() durableSinkState {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var state durableSinkState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	for id := uint64(1); id <= 2; id++ {
		want := int(id) * 300
		writeLimit(want)
		waitFor(t, 4*time.Second, func() bool { state := readState(); return len(state.Active) == 300 })
		cp, err := coord.TriggerCheckpoint(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cp.ID != id {
			t.Fatalf("checkpoint %d, want %d", cp.ID, id)
		}
		waitFor(t, 4*time.Second, func() bool { return len(readState().Visible) == want })
	}
	// Park at the final offset so the explicit third checkpoint is the kill boundary.
	writeLimit(1000)
	waitFor(t, 4*time.Second, func() bool { return len(readState().Active) == 400 })
	cp, err := coord.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cp.ID != 3 {
		t.Fatal(cp.ID)
	}
	waitFor(t, 4*time.Second, func() bool { _, err := os.Stat(filepath.Join(ledger, "prepared-third")); return err == nil })
	if state := readState(); len(state.Visible) != 600 || len(state.Prepared[3]) != 400 {
		t.Fatalf("prepare changed visibility: %+v", state)
	}
	if err := processes[owner].Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = processes[owner].Wait()
	delete(processes, owner)
	writeLimit(600)
	if err := os.Remove(filepath.Join(ledger, "pause-third")); err != nil {
		t.Fatal(err)
	}
	// Same worker identity, fresh process; completed archive remains on its peer.
	start(owner)
	waitFor(t, 10*time.Second, func() bool { state := readState(); return state.Generation > 1 && len(state.Prepared) == 0 })
	if state := readState(); len(state.Visible) != 600 || len(state.Active) != 0 {
		t.Fatalf("recovery did not preserve exactly two committed intervals: %+v", state)
	}
	writeLimit(1001)
	waitFor(t, 10*time.Second, func() bool { job, err := coord.GetJob(job.ID); return err == nil && job.Status.IsTerminal() })
	completed, err := coord.GetJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != coordinator.JobFinished {
		t.Fatalf("recovery failed: %+v", completed)
	}
	state := readState()
	seen := map[string]bool{}
	for _, record := range state.Visible {
		if seen[record] {
			t.Fatalf("duplicate replayed record %s", record)
		}
		seen[record] = true
	}
	if len(seen) != 1000 || len(state.Prepared) != 0 || len(state.Active) != 0 {
		t.Fatalf("recovery output: visible=%d prepared=%d active=%d", len(seen), len(state.Prepared), len(state.Active))
	}
}
