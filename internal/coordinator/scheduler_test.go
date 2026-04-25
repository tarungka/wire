package coordinator

import (
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// linearGraph builds a JobGraph for "src -> m -> snk" with forward edges.
func linearGraph() rpc.JobGraph {
	return rpc.JobGraph{
		Operators: []rpc.OperatorDescriptor{
			{OperatorID: "src", Type: rpc.OperatorTypeSource, ClassName: "memory-source"},
			{OperatorID: "m", Type: rpc.OperatorTypeMap, ClassName: "upper"},
			{OperatorID: "snk", Type: rpc.OperatorTypeSink, ClassName: "memory-sink"},
		},
		Edges: []rpc.EdgeDescriptor{
			{SourceOperatorID: "src", TargetOperatorID: "m", Shuffle: rpc.ShuffleStrategyForward},
			{SourceOperatorID: "m", TargetOperatorID: "snk", Shuffle: rpc.ShuffleStrategyForward},
		},
	}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := protocol.EncodeMsgPack(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

func TestGenerateTaskDescriptors_LinearGraph(t *testing.T) {
	graph := linearGraph()
	job := &JobMeta{
		ID:          "job-1",
		Parallelism: 2,
		Config:      encode(t, graph),
	}

	tasks, err := generateTaskDescriptors(job)
	if err != nil {
		t.Fatalf("generateTaskDescriptors: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks (parallelism=2), got %d", len(tasks))
	}

	// Each task carries the full topo-sorted chain (Source, Map, Sink).
	for i, td := range tasks {
		if got, want := len(td.OperatorChain), 3; got != want {
			t.Fatalf("task %d: chain len got %d want %d", i, got, want)
		}
		ids := []string{td.OperatorChain[0].OperatorID, td.OperatorChain[1].OperatorID, td.OperatorChain[2].OperatorID}
		want := []string{"src", "m", "snk"}
		for k := range want {
			if ids[k] != want[k] {
				t.Fatalf("task %d: chain ids = %v, want %v", i, ids, want)
			}
		}

		// Subtask index reflects ordinal; primary OperatorID is the first
		// non-source operator in the chain.
		if td.SubtaskIndex != int32(i) {
			t.Errorf("task %d: SubtaskIndex got %d want %d", i, td.SubtaskIndex, i)
		}
		if td.OperatorID != "m" {
			t.Errorf("task %d: OperatorID got %q want %q", i, td.OperatorID, "m")
		}
		if td.Parallelism != 2 {
			t.Errorf("task %d: Parallelism got %d want %d", i, td.Parallelism, 2)
		}
	}

	// KeyGroup ranges must cover [0, 127] without overlap.
	if tasks[0].KeyGroup.Start != 0 {
		t.Errorf("first KeyGroup start = %d, want 0", tasks[0].KeyGroup.Start)
	}
	if tasks[len(tasks)-1].KeyGroup.End != 127 {
		t.Errorf("last KeyGroup end = %d, want 127", tasks[len(tasks)-1].KeyGroup.End)
	}
	if tasks[0].KeyGroup.End+1 != tasks[1].KeyGroup.Start {
		t.Errorf("KeyGroup ranges not contiguous: %v then %v", tasks[0].KeyGroup, tasks[1].KeyGroup)
	}
}

func TestGenerateTaskDescriptors_RejectsEmptyConfig(t *testing.T) {
	job := &JobMeta{ID: "job-x", Parallelism: 1, Config: nil}
	_, err := generateTaskDescriptors(job)
	if err == nil {
		t.Fatal("expected error for empty config")
	}
	if !strings.Contains(err.Error(), "no graph") && !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error %q should mention empty/missing graph", err)
	}
}

func TestGenerateTaskDescriptors_RejectsBadConfig(t *testing.T) {
	job := &JobMeta{ID: "job-x", Parallelism: 1, Config: []byte{0xff, 0xfe, 0xfd, 0xfc}}
	_, err := generateTaskDescriptors(job)
	if err == nil {
		t.Fatal("expected decode error for garbage config")
	}
}

func TestGenerateTaskDescriptors_RejectsShuffleEdge(t *testing.T) {
	graph := linearGraph()
	graph.Edges[0].Shuffle = rpc.ShuffleStrategyHash // first edge becomes a shuffle
	job := &JobMeta{ID: "job-shuffle", Parallelism: 1, Config: encode(t, graph)}

	_, err := generateTaskDescriptors(job)
	if err == nil {
		t.Fatal("expected error for shuffle edge in Phase 1")
	}
	if !strings.Contains(err.Error(), "shuffle") && !strings.Contains(err.Error(), "Phase 2") {
		t.Fatalf("error %q should mention shuffle/Phase 2", err)
	}
}

func TestGenerateTaskDescriptors_RejectsCycle(t *testing.T) {
	graph := rpc.JobGraph{
		Operators: []rpc.OperatorDescriptor{
			{OperatorID: "a", Type: rpc.OperatorTypeMap, ClassName: "x"},
			{OperatorID: "b", Type: rpc.OperatorTypeMap, ClassName: "x"},
		},
		Edges: []rpc.EdgeDescriptor{
			{SourceOperatorID: "a", TargetOperatorID: "b", Shuffle: rpc.ShuffleStrategyForward},
			{SourceOperatorID: "b", TargetOperatorID: "a", Shuffle: rpc.ShuffleStrategyForward},
		},
	}
	job := &JobMeta{ID: "job-cycle", Parallelism: 1, Config: encode(t, graph)}

	_, err := generateTaskDescriptors(job)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error %q should mention cycle", err)
	}
}

func TestGenerateTaskDescriptors_RejectsDanglingEdge(t *testing.T) {
	graph := rpc.JobGraph{
		Operators: []rpc.OperatorDescriptor{
			{OperatorID: "a", Type: rpc.OperatorTypeSource, ClassName: "src"},
			{OperatorID: "b", Type: rpc.OperatorTypeSink, ClassName: "snk"},
		},
		Edges: []rpc.EdgeDescriptor{
			// References operator "ghost" that is not declared.
			{SourceOperatorID: "a", TargetOperatorID: "ghost", Shuffle: rpc.ShuffleStrategyForward},
		},
	}
	job := &JobMeta{ID: "job-dangling", Parallelism: 1, Config: encode(t, graph)}

	_, err := generateTaskDescriptors(job)
	if err == nil {
		t.Fatal("expected error for edge referencing unknown operator")
	}
}

// TestScheduleJob_BadConfigGoesToFailed verifies that a job whose Config
// can't be decoded (e.g. legacy or pre-Phase-1 submission) is transitioned
// all the way to JobFailed instead of being left in JobFailing forever.
func TestScheduleJob_BadConfigGoesToFailed(t *testing.T) {
	store := NewMemoryStore()
	c := New(CoordinatorConfig{NodeID: "n1", ListenAddr: ":0"}, store, nil, zerolog.Nop())

	// Manually drive the coordinator into the leader state without starting
	// the scheduler goroutine — we want to call scheduleJob directly.
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.mu.Unlock()

	job := &JobMeta{
		ID:          "job-bad",
		Name:        "legacy",
		Status:      JobCreated,
		Parallelism: 1,
		Config:      []byte("not msgpack"),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	c.mu.Lock()
	c.jobs[job.ID] = job
	c.mu.Unlock()
	if err := c.persistJob(job); err != nil {
		t.Fatalf("persistJob: %v", err)
	}

	c.scheduleJob(job)

	if job.Status != JobFailed {
		t.Fatalf("status = %s, want %s", job.Status, JobFailed)
	}
	if job.FinishedAt.IsZero() {
		t.Errorf("FinishedAt should be set on terminal state")
	}
}

func TestTopoSortOperators_DeterministicOrder(t *testing.T) {
	// Two valid topo orderings exist for "a -> c, b -> c"; we want stable
	// output that follows the input order of graph.Operators (a, b, c).
	graph := rpc.JobGraph{
		Operators: []rpc.OperatorDescriptor{
			{OperatorID: "a", Type: rpc.OperatorTypeSource, ClassName: "x"},
			{OperatorID: "b", Type: rpc.OperatorTypeSource, ClassName: "x"},
			{OperatorID: "c", Type: rpc.OperatorTypeSink, ClassName: "x"},
		},
		Edges: []rpc.EdgeDescriptor{
			{SourceOperatorID: "a", TargetOperatorID: "c", Shuffle: rpc.ShuffleStrategyForward},
			{SourceOperatorID: "b", TargetOperatorID: "c", Shuffle: rpc.ShuffleStrategyForward},
		},
	}
	for i := 0; i < 10; i++ {
		sorted, err := topoSortOperators(graph)
		if err != nil {
			t.Fatalf("topoSort: %v", err)
		}
		ids := []string{sorted[0].OperatorID, sorted[1].OperatorID, sorted[2].OperatorID}
		want := []string{"a", "b", "c"}
		for k := range want {
			if ids[k] != want[k] {
				t.Fatalf("iteration %d: order = %v, want %v", i, ids, want)
			}
		}
	}
}
