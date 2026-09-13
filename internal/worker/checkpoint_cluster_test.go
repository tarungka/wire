package worker_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

type checkpointTestSource struct{}

func (*checkpointTestSource) Open(context.Context) error        { return nil }
func (*checkpointTestSource) Close() error                      { return nil }
func (*checkpointTestSource) Checkpoint(uint64) ([]byte, error) { return []byte("source-offset"), nil }
func (*checkpointTestSource) GenerateWatermark() int64          { return 0 }
func (*checkpointTestSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return []engine.Event{{Value: []byte("record")}}, nil
	}
}

func TestClusterCheckpointReplicatesAndCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "coordinator"}, coordinator.NewMemoryStore(), nil, zerolog.Nop())
	coordDone := make(chan error, 1)
	go func() { coordDone <- coord.Run(ctx) }()
	waitFor(t, 2*time.Second, coord.IsReady)
	server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(ctx) }()
	defer func() {
		cancel()
		_ = server.Shutdown(context.Background())
		if err := <-serverDone; err != nil {
			t.Error(err)
		}
	}()
	registry := worker.NewRegistry()
	registry.RegisterSource("checkpoint-source", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
		return &checkpointTestSource{}, nil
	})
	var stores []string
	var workers []*worker.Worker
	var done []chan error
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		stores = append(stores, root)
		w := worker.NewWithRegistry(worker.Config{WorkerID: fmt.Sprintf("worker-%d", i), CoordinatorAddr: server.Addr(), TaskSlots: 1, CheckpointReplica: &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: root, ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 1}}, registry, zerolog.Nop())
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
		<-coordDone
	}()
	waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
	graph, err := protocol.EncodeMsgPack(rpc.JobGraph{Operators: []rpc.OperatorDescriptor{{OperatorID: "source", ClassName: "checkpoint-source", Type: rpc.OperatorTypeSource}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := coord.SubmitJob("checkpoint", 1, graph)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		current, err := coord.GetJob(job.ID)
		return err == nil && current.Status == coordinator.JobRunning
	})
	checkpoint, err := coord.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 4*time.Second, func() bool {
		current, err := coord.GetJob(job.ID)
		return err == nil && current.LatestCheckpoint == checkpoint.ID
	})
	for taskID, owner := range checkpoint.Tasks {
		index := 1
		if owner == "worker-1" {
			index = 0
		}
		store, err := engine.NewFileCheckpointStore(stores[index])
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.Get(ctx, job.ID, taskID, checkpoint.ID, checkpoint.EpochID)
		if err != nil || string(snapshot.Source) != "source-offset" {
			t.Fatalf("remote snapshot: %+v, %v", snapshot, err)
		}
	}
}
