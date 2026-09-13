package worker_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

type shuffleSource struct {
	checkpointTestSource
	index, next int
	unkeyed     bool
}

func (s *shuffleSource) ReadBatch(context.Context) ([]engine.Event, error) {
	if s.next == 128 {
		return nil, nil
	}
	i := s.next
	s.next++
	key := []byte(fmt.Sprintf("key-%d", i))
	if i == 0 || s.unkeyed {
		key = nil
	}
	return []engine.Event{{Key: key, Value: []byte(fmt.Sprintf("%d:%d", s.index, i))}}, nil
}

type shuffleSink struct {
	checkpointTestSource
	write func(engine.Event) error
}

func (s *shuffleSink) Write(_ context.Context, event engine.Event) error { return s.write(event) }

func TestClusterHashShuffleRoutesEveryRecord(t *testing.T) { testClusterHashShuffle(t, false) }
func TestClusterKeyBySelectsBeforeShuffle(t *testing.T)    { testClusterHashShuffle(t, true) }

func testClusterHashShuffle(t *testing.T, selectKeys bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	metadata := coordinator.NewMemoryStore()
	defer metadata.Close()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "coordinator"}, metadata, nil, zerolog.Nop())
	coordDone := make(chan error, 1)
	go func() { coordDone <- coord.Run(ctx) }()
	waitFor(t, 2*time.Second, coord.IsReady)
	server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(ctx) }()
	registry := worker.NewRegistry()
	var mu sync.Mutex
	seen := make(map[string]bool)
	registry.RegisterSource("shuffle-source", func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.SourceOperator, error) {
		return &shuffleSource{index: int(tc.SubtaskIndex), unkeyed: selectKeys}, nil
	})
	registry.RegisterKeyBy("select-key", func(context.Context, []byte, worker.TaskContext) (worker.KeySelector, error) {
		return func(_ context.Context, event engine.Event) ([]byte, error) {
			var source, index int
			if _, err := fmt.Sscanf(string(event.Value), "%d:%d", &source, &index); err != nil {
				return nil, err
			}
			if index == 0 {
				return nil, nil
			}
			return []byte(fmt.Sprintf("key-%d", index)), nil
		}, nil
	})
	registry.RegisterSink("shuffle-sink", func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.SinkOperator, error) {
		return &shuffleSink{write: func(event engine.Event) error {
			var source, index int
			if _, err := fmt.Sscanf(string(event.Value), "%d:%d", &source, &index); err != nil {
				return err
			}
			expectedKey := ""
			if index != 0 {
				expectedKey = fmt.Sprintf("key-%d", index)
			}
			if string(event.Key) != expectedKey {
				return fmt.Errorf("selected key %q, want %q", event.Key, expectedKey)
			}
			group := int(keygroup.KeyGroup(event.Key, 128))
			owner := int(tc.SubtaskIndex)
			if group < owner*128/3 || group >= (owner+1)*128/3 {
				return fmt.Errorf("group %d reached task %d", group, owner)
			}
			mu.Lock()
			defer mu.Unlock()
			id := string(event.Value)
			if seen[id] {
				return fmt.Errorf("duplicate %s", id)
			}
			seen[id] = true
			return nil
		}}, nil
	})
	var workers []*worker.Worker
	var done []chan error
	defer func() {
		cancel()
		for _, w := range workers {
			_ = w.Shutdown(context.Background())
		}
		for _, ch := range done {
			<-ch
		}
		_ = server.Shutdown(context.Background())
		<-serverDone
		<-coordDone
	}()
	for i := 0; i < 2; i++ {
		w := worker.NewWithRegistry(worker.Config{WorkerID: fmt.Sprintf("shuffle-worker-%d", i), CoordinatorAddr: server.Addr(), TaskSlots: 3}, registry, zerolog.Nop())
		workers = append(workers, w)
		ch := make(chan error, 1)
		done = append(done, ch)
		go func() { ch <- w.Run(ctx) }()
	}
	waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
	graphSpec := rpc.JobGraph{NumKeyGroups: 128, Operators: []rpc.OperatorDescriptor{{OperatorID: "source", ClassName: "shuffle-source", Type: rpc.OperatorTypeSource, Parallelism: 2}, {OperatorID: "sink", ClassName: "shuffle-sink", Type: rpc.OperatorTypeSink, Parallelism: 3}}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyHash}}}
	if selectKeys {
		graphSpec.Operators = append(graphSpec.Operators, rpc.OperatorDescriptor{OperatorID: "select", ClassName: "select-key", Type: rpc.OperatorTypeKeyBy, Parallelism: 2})
		graphSpec.Edges = []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "select", Shuffle: rpc.ShuffleStrategyForward}, {SourceOperatorID: "select", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyHash}}
	}
	graph, err := protocol.EncodeMsgPack(graphSpec)
	if err != nil {
		t.Fatal(err)
	}
	job, err := coord.SubmitJob("shuffle", 3, graph)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 8*time.Second, func() bool {
		current, err := coord.GetJob(job.ID)
		return err == nil && current.Status == coordinator.JobFinished
	})
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 256 {
		t.Fatalf("received %d records, want 256", len(seen))
	}
}
