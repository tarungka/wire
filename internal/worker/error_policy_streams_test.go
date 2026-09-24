package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

type nthPoisonMap struct {
	lifecycleProbe
	calls int
}

func (m *nthPoisonMap) Map(_ context.Context, e engine.Event) (engine.Event, error) {
	m.calls++
	if m.calls == 50 {
		return e, errors.New("poison record")
	}
	return e, nil
}

func TestErrorPolicyAcrossWorkerStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	newMux := func(id string) *transport.Mux {
		cfg := transport.DefaultConfig()
		cfg.NodeID, cfg.ListenAddr = id, "127.0.0.1:0"
		mux := transport.NewMux(cfg)
		if err := mux.Listen(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mux.Close() })
		return mux
	}
	producer, consumer := newMux("producer"), newMux("consumer")
	var running atomic.Bool
	source := &lifecycleSource{remaining: 100, running: &running}
	main, dlq := &lifecycleSink{}, &policySink{}
	reg, chain := lifecyclePipeline(source, &lifecycleMap{}, main)
	reg.RegisterMap("parse", func(context.Context, []byte, TaskContext) (engine.MapOperator, error) { return &nthPoisonMap{}, nil })
	reg.RegisterSink("dlq", func(context.Context, []byte, TaskContext) (engine.SinkOperator, error) { return dlq, nil })
	parse := &chain.OperatorChain[1]
	parse.ClassName, parse.OperatorID = "parse", "parse"
	parse.ErrorPolicy = &rpc.ErrorPolicy{OnExhausted: "dlq"}
	parse.DLQSink = &rpc.DLQSinkDescriptor{ClassName: "dlq"}
	src, dst := newTaskExecutor(reg), newTaskExecutor(reg)
	src.data, dst.data = producer, consumer
	const sourceID, sinkID = "job/source/0", "job/sink/0"
	descriptor := rpc.TaskDescriptor{TaskID: sinkID, OperatorChain: chain.OperatorChain[1:], Upstream: []rpc.UpstreamChannelInfo{{TaskID: sourceID}}}
	encoded, err := protocol.EncodeMsgPack(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	var decoded rpc.TaskDescriptor
	if err := protocol.DecodeMsgPack(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- dst.run(ctx, "job", sinkID, decoded, zerolog.Nop(), nil) }()
	sourceDesc := rpc.TaskDescriptor{TaskID: sourceID, OperatorChain: chain.OperatorChain[:1], Downstream: []rpc.DownstreamChannelInfo{{TaskID: sinkID, Address: consumer.ListenAddr()}}}
	if err := src.run(ctx, "job", sourceID, sourceDesc, zerolog.Nop(), func() { running.Store(true) }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if main.count.Load() != 99 || len(dlq.events) != 1 || !dlq.opened || !dlq.closed {
		t.Fatalf("main=%d DLQ=%+v", main.count.Load(), dlq)
	}
	if consumer.IsTaskRegistered(sinkID) {
		t.Fatal("finished task still registered")
	}
}
