package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

// This test backend atomically persists transaction state and visible output.
// Each logical sink task has its own file; the mutex models server-side atomicity.
type durableSinkLedger struct {
	mu   sync.Mutex
	root string
}
type durableSinkState struct {
	Generation uint64
	Attempt    string
	Active     []string
	Prepared   map[uint64][]string
	Committed  map[uint64]bool
	Visible    []string
}
type durableClusterSink struct {
	lostCommitResponses int
	afterPrepare        func(context.Context, uint64) error
	ledger              *durableSinkLedger
	task                string
	authority           engine.TransactionRecovery
	prepared            uint64
}

func (s *durableClusterSink) mutate(fn func(*durableSinkState) error) error {
	s.ledger.mu.Lock()
	defer s.ledger.mu.Unlock()
	path := filepath.Join(s.ledger.root, fmt.Sprintf("%x.json", s.task))
	state := durableSinkState{Prepared: map[uint64][]string{}, Committed: map[uint64]bool{}}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return err
		}
	}
	if err = fn(&state); err != nil {
		return err
	}
	data, err = json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(path+".next", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".next", path)
}
func (s *durableClusterSink) fenced(fn func(*durableSinkState) error) error {
	return s.mutate(func(state *durableSinkState) error {
		if state.Generation != s.authority.DeploymentGeneration || state.Attempt != s.authority.AttemptID {
			return fmt.Errorf("obsolete writer")
		}
		return fn(state)
	})
}
func (*durableClusterSink) Open(context.Context) error { return nil }
func (*durableClusterSink) Close() error               { return nil }
func (s *durableClusterSink) RecoverTransactions(_ context.Context, authority engine.TransactionRecovery) error {
	err := s.mutate(func(state *durableSinkState) error {
		if authority.DeploymentGeneration < state.Generation || (authority.DeploymentGeneration == state.Generation && authority.AttemptID != state.Attempt) {
			return fmt.Errorf("obsolete recovery")
		}
		state.Generation, state.Attempt = authority.DeploymentGeneration, authority.AttemptID
		state.Active = nil
		for id := range state.Prepared {
			if id != authority.CompletedCheckpointID {
				delete(state.Prepared, id)
			}
		}
		return nil
	})
	if err == nil {
		s.authority = authority
	}
	return err
}
func (s *durableClusterSink) BeginTransaction(context.Context) error {
	return s.fenced(func(state *durableSinkState) error { state.Active = nil; return nil })
}
func (s *durableClusterSink) Write(_ context.Context, e engine.Event) error {
	return s.fenced(func(state *durableSinkState) error { state.Active = append(state.Active, string(e.Value)); return nil })
}
func (s *durableClusterSink) PreCommit(ctx context.Context, id uint64) error {
	err := s.fenced(func(state *durableSinkState) error { state.Prepared[id] = state.Active; state.Active = nil; return nil })
	if err == nil {
		s.prepared = id
		if s.afterPrepare != nil {
			return s.afterPrepare(ctx, id)
		}
	}
	return err
}
func (s *durableClusterSink) Commit(_ context.Context, id uint64) error {
	err := s.fenced(func(state *durableSinkState) error {
		if state.Committed[id] {
			return nil
		}
		records, ok := state.Prepared[id]
		if !ok {
			return fmt.Errorf("missing transaction %d", id)
		}
		state.Visible = append(state.Visible, records...)
		state.Committed[id] = true
		delete(state.Prepared, id)
		return nil
	})
	if err == nil && s.lostCommitResponses > 0 {
		s.lostCommitResponses--
		return fmt.Errorf("commit applied but response lost")
	}
	return err
}
func (s *durableClusterSink) Abort(context.Context) error {
	return s.fenced(func(state *durableSinkState) error {
		state.Active = nil
		delete(state.Prepared, s.prepared)
		return nil
	})
}
func (s *durableClusterSink) Checkpoint(uint64) ([]byte, error) { return json.Marshal(s.prepared) }
func (s *durableClusterSink) RestoreCheckpoint(data []byte) error {
	return json.Unmarshal(data, &s.prepared)
}

type boundedCheckpointSource struct {
	subtask int32
	next    int
}

func (*boundedCheckpointSource) Open(context.Context) error          { return nil }
func (*boundedCheckpointSource) Close() error                        { return nil }
func (*boundedCheckpointSource) GenerateWatermark() int64            { return 0 }
func (s *boundedCheckpointSource) Checkpoint(uint64) ([]byte, error) { return json.Marshal(s.next) }
func (s *boundedCheckpointSource) RestoreCheckpoint(data []byte) error {
	return json.Unmarshal(data, &s.next)
}
func (s *boundedCheckpointSource) ReadBatch(context.Context) ([]engine.Event, error) {
	if s.next == 500 {
		return nil, nil
	}
	events := make([]engine.Event, 10)
	for i := range events {
		events[i] = engine.Event{Value: []byte(fmt.Sprintf("%d/%d", s.subtask, s.next))}
		s.next++
	}
	return events, nil
}

func TestBoundedTransactionalJobCommitsFinalRecords(t *testing.T) {
	testBoundedTransactionalJob(t, false)
}
func TestTransactionalCommitRetriesLostResponseWithoutDuplicates(t *testing.T) {
	testBoundedTransactionalJob(t, true)
}

func testBoundedTransactionalJob(t *testing.T, loseResponses bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	store := coordinator.NewMemoryStore()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "coordinator", CheckpointMinPause: time.Hour}, store, nil, zerolog.Nop())
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
	ledger := &durableSinkLedger{root: t.TempDir()}
	registry := worker.NewRegistry()
	registry.RegisterSource("bounded", func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.SourceOperator, error) {
		return &boundedCheckpointSource{subtask: tc.SubtaskIndex}, nil
	})
	registry.RegisterSink("durable", func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.SinkOperator, error) {
		sink := &durableClusterSink{ledger: ledger, task: tc.TaskID}
		if loseResponses {
			sink.lostCommitResponses = 2
		}
		return sink, nil
	})
	var workers []*worker.Worker
	var done []chan error
	for i := 0; i < 2; i++ {
		replica := &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 2}
		w := worker.NewWithRegistry(worker.Config{WorkerID: fmt.Sprint(i), CoordinatorAddr: server.Addr(), TaskSlots: 2, CheckpointReplica: replica}, registry, zerolog.Nop())
		workers = append(workers, w)
		ch := make(chan error, 1)
		done = append(done, ch)
		go func() { ch <- w.Run(ctx) }()
	}
	defer func() {
		cancel()
		for _, w := range workers {
			_ = w.Shutdown(context.Background())
		}
		for _, ch := range done {
			<-ch
		}
	}()
	waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
	graph, err := protocol.EncodeMsgPack(rpc.JobGraph{Operators: []rpc.OperatorDescriptor{{OperatorID: "source", Type: rpc.OperatorTypeSource, ClassName: "bounded"}, {OperatorID: "sink", Type: rpc.OperatorTypeSink, ClassName: "durable"}}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyRebalance}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := coord.SubmitJob("bounded-final", 2, graph)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 12*time.Second, func() bool { job, err := coord.GetJob(job.ID); return err == nil && job.Status.IsTerminal() })
	completed, err := coord.GetJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != coordinator.JobFinished || completed.LatestCheckpoint == 0 {
		t.Fatalf("final transaction not completed: %+v", completed)
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entries, err := os.ReadDir(ledger.root)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(ledger.root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var state durableSinkState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		if len(state.Prepared) != 0 || len(state.Active) != 0 {
			t.Fatal("successful job left uncommitted output")
		}
		for _, value := range state.Visible {
			if seen[value] {
				t.Fatalf("duplicate output %s", value)
			}
			seen[value] = true
		}
	}
	if len(seen) != 1000 {
		t.Fatalf("visible final records: %d, want 1000", len(seen))
	}
}
