package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

func TestTaskExecutorProcessesAcrossWorkerStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	newMux := func(id string) *transport.Mux {
		cfg := transport.DefaultConfig()
		cfg.NodeID = id
		cfg.ListenAddr = "127.0.0.1:0"
		mux := transport.NewMux(cfg)
		if err := mux.Listen(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mux.Close() })
		return mux
	}
	upstream, downstream := newMux("upstream-worker"), newMux("downstream-worker")
	var running atomic.Bool
	const records = 2048
	source := &lifecycleSource{remaining: records, running: &running}
	mapping, sink := &lifecycleMap{}, &lifecycleSink{}
	registry, chain := lifecyclePipeline(source, mapping, sink)
	sourceExecutor, sinkExecutor := newTaskExecutor(registry), newTaskExecutor(registry)
	sourceExecutor.data = upstream
	sinkExecutor.data = downstream
	sourceID, sinkID := "job/source/0", "job/sink/0"
	sinkDescriptor := rpc.TaskDescriptor{TaskID: sinkID, OperatorChain: chain.OperatorChain[1:], Upstream: []rpc.UpstreamChannelInfo{{TaskID: sourceID}}}
	sinkDone := make(chan error, 1)
	go func() { sinkDone <- sinkExecutor.run(ctx, "job", sinkID, sinkDescriptor, zerolog.Nop(), func() {}) }()
	for !downstream.IsTaskRegistered(sinkID) {
		select {
		case err := <-sinkDone:
			t.Fatalf("downstream startup: %v", err)
		case <-ctx.Done():
			t.Fatal("downstream did not register its input")
		case <-time.After(time.Millisecond):
		}
	}
	sourceDescriptor := rpc.TaskDescriptor{TaskID: sourceID, OperatorChain: chain.OperatorChain[:1], Downstream: []rpc.DownstreamChannelInfo{{TaskID: sinkID, Address: downstream.ListenAddr()}}}
	if err := sourceExecutor.run(ctx, "job", sourceID, sourceDescriptor, zerolog.Nop(), func() { running.Store(true) }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-sinkDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("network sink did not terminate")
	}
	if got := sink.count.Load(); got != records {
		t.Fatalf("sink received %d of %d records", got, records)
	}
	for _, probe := range []*lifecycleProbe{&source.lifecycleProbe, &mapping.lifecycleProbe, &sink.lifecycleProbe} {
		if probe.opened.Load() != 1 || probe.closed.Load() != 1 {
			t.Fatalf("operator lifecycle open=%d close=%d", probe.opened.Load(), probe.closed.Load())
		}
	}
	if downstream.IsTaskRegistered(sinkID) {
		t.Fatal("finished task left a stale routing registration")
	}
}
