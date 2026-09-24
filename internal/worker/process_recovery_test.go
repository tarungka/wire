package worker_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
	"github.com/tarungka/wire/sdk"
)

type processObservations struct {
	mu     sync.Mutex
	values map[string]int
}

func (o *processObservations) count(value string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.values[value]
}

type processSink struct {
	checkpointTestSource
	observations *processObservations
	prefix       string
}

func (*processSink) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (s *processSink) Write(_ context.Context, e engine.Event) error {
	s.observations.mu.Lock()
	defer s.observations.mu.Unlock()
	s.observations.values[s.prefix+string(e.Value)]++
	return nil
}

func TestClusterProcessStateTimerAndSideOutputRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "process-coordinator"}, coordinator.NewMemoryStore(), nil, zerolog.Nop())
	coordDone := make(chan error, 1)
	go func() { coordDone <- coord.Run(ctx) }()
	waitFor(t, 2*time.Second, coord.IsReady)
	server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(ctx) }()
	var phase, instances atomic.Int32
	phase.Store(1)
	var fail, restored atomic.Bool
	observations := &processObservations{values: make(map[string]int)}
	defer func() {
		if t.Failed() {
			observations.mu.Lock()
			t.Logf("observed: %v", observations.values)
			observations.mu.Unlock()
		}
	}()
	reg := worker.NewRegistry()
	reg.RegisterSource("source", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
		return &lateRecoverySource{phase: &phase, fail: &fail, restored: &restored, needsRestore: instances.Add(1) > 1}, nil
	})
	reg.RegisterProcess("process", func(context.Context, []byte, worker.TaskContext) (engine.FlatMapOperator, error) {
		return sdk.NewProcessOperator(func(c sdk.ProcessContext, e sdk.Event) ([]sdk.Event, error) {
			if string(e.Key) != "k" {
				return nil, nil
			}
			state := c.GetState("count")
			n, err := state.ValueInt64()
			if err != nil {
				return nil, err
			}
			if err := state.SetInt64(n + 1); err != nil {
				return nil, err
			}
			c.RegisterEventTimeTimer(21)
			c.EmitToSideOutput(sdk.NewOutputTag("audit"), e)
			return []sdk.Event{{Key: e.Key, Value: []byte(fmt.Sprintf("count%d", n+1))}}, nil
		}, func(c sdk.ProcessContext, ts int64) ([]sdk.Event, error) {
			n, err := c.GetState("count").ValueInt64()
			if err != nil {
				return nil, err
			}
			return []sdk.Event{{Key: c.Key(), Value: []byte(fmt.Sprintf("timer%d", n)), EventTime: ts}}, nil
		}, sdk.NewPebbleStateBackend("")), nil
	})
	for _, prefix := range []string{"main/", "audit/"} {
		reg.RegisterSink(prefix, func(context.Context, []byte, worker.TaskContext) (engine.SinkOperator, error) {
			return &processSink{observations: observations, prefix: prefix}, nil
		})
	}
	var workers []*worker.Worker
	var done []chan error
	for i := 0; i < 2; i++ {
		w := worker.NewWithRegistry(worker.Config{WorkerID: fmt.Sprint("process-worker-", i), CoordinatorAddr: server.Addr(), TaskSlots: 3, CheckpointReplica: &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 4}}, reg, zerolog.Nop())
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
		_ = server.Shutdown(context.Background())
		<-serverDone
		<-coordDone
	}()
	waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
	graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{
		{OperatorID: "source", ClassName: "source", Type: rpc.OperatorTypeSource, Parallelism: 1, Watermark: &rpc.WatermarkConfig{Strategy: "monotonic", EmitInterval: time.Millisecond}},
		{OperatorID: "process", ClassName: "process", Type: rpc.OperatorTypeProcess, Parallelism: 2, SideOutputTags: []string{"audit"}},
		{OperatorID: "main", ClassName: "main/", Type: rpc.OperatorTypeSink, Parallelism: 1},
		{OperatorID: "audit", ClassName: "audit/", Type: rpc.OperatorTypeSink, Parallelism: 1},
	}, Edges: []rpc.EdgeDescriptor{
		{SourceOperatorID: "source", TargetOperatorID: "process", Shuffle: rpc.ShuffleStrategyHash},
		{SourceOperatorID: "process", TargetOperatorID: "main", Shuffle: rpc.ShuffleStrategyRebalance},
		{SourceOperatorID: "process", TargetOperatorID: "audit", Shuffle: rpc.ShuffleStrategyRebalance, SideOutput: "audit"},
	}}
	data, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		t.Fatal(err)
	}
	job, err := coord.SubmitJob("process-recovery", 1, data)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return observations.count("main/count1") == 1 && observations.count("audit/first") == 1 })
	checkpoint, err := coord.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { j, e := coord.GetJob(job.ID); return e == nil && j.LatestCheckpoint == checkpoint.ID })
	for attempt := 1; attempt <= 2; attempt++ {
		phase.Store(2)
		waitFor(t, 5*time.Second, func() bool {
			return observations.count("main/count2") == attempt && observations.count("audit/update") == attempt
		})
		phase.Store(3)
		waitFor(t, 5*time.Second, func() bool { return observations.count("main/timer2") == attempt })
		if attempt == 1 {
			phase.Store(1)
			fail.Store(true)
			waitFor(t, 10*time.Second, func() bool { j, e := coord.GetJob(job.ID); return e == nil && j.RestartCount == 1 && restored.Load() })
		}
	}
	if observations.count("main/count1") != 1 || observations.count("main/timer1") != 0 {
		t.Fatal("recovery lost source offset or managed state")
	}
	second, err := coord.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { j, e := coord.GetJob(job.ID); return e == nil && j.LatestCheckpoint == second.ID })
}
