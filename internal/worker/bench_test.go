package worker

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// benchTC returns a no-op TaskContext for benchmarks.
func benchTC() TaskContext { return TaskContext{Log: zerolog.Nop()} }

// benchSrc / benchMap / benchFlatMap / benchSink mirror the registry-test
// stubs but are visible to the bench file. They do nothing so the bench
// measures registry lookup + factory invocation, not operator work.
type benchSrc struct{}

func (*benchSrc) Open(_ context.Context) error                        { return nil }
func (*benchSrc) Close() error                                        { return nil }
func (*benchSrc) Checkpoint(_ uint64) ([]byte, error)                 { return nil, nil }
func (*benchSrc) ReadBatch(_ context.Context) ([]engine.Event, error) { return nil, nil }
func (*benchSrc) GenerateWatermark() int64                            { return 0 }

type benchMap struct{}

func (*benchMap) Open(_ context.Context) error                                { return nil }
func (*benchMap) Close() error                                                { return nil }
func (*benchMap) Checkpoint(_ uint64) ([]byte, error)                         { return nil, nil }
func (*benchMap) Map(_ context.Context, e engine.Event) (engine.Event, error) { return e, nil }

type benchFlatMap struct{}

func (*benchFlatMap) Open(_ context.Context) error        { return nil }
func (*benchFlatMap) Close() error                        { return nil }
func (*benchFlatMap) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (*benchFlatMap) FlatMap(_ context.Context, _ engine.Event, _ func(engine.Event)) error {
	return nil
}

type benchSink struct{}

func (*benchSink) Open(_ context.Context) error                  { return nil }
func (*benchSink) Close() error                                  { return nil }
func (*benchSink) Checkpoint(_ uint64) ([]byte, error)           { return nil, nil }
func (*benchSink) Write(_ context.Context, _ engine.Event) error { return nil }

// newBenchRegistry returns a registry with one factory of each kind.
func newBenchRegistry() *Registry {
	r := NewRegistry()
	r.RegisterSource("src", func(_ context.Context, _ []byte, _ TaskContext) (engine.SourceOperator, error) {
		return &benchSrc{}, nil
	})
	r.RegisterMap("m", func(_ context.Context, _ []byte, _ TaskContext) (engine.MapOperator, error) {
		return &benchMap{}, nil
	})
	r.RegisterFlatMap("fm", func(_ context.Context, _ []byte, _ TaskContext) (engine.FlatMapOperator, error) {
		return &benchFlatMap{}, nil
	})
	r.RegisterSink("snk", func(_ context.Context, _ []byte, _ TaskContext) (engine.SinkOperator, error) {
		return &benchSink{}, nil
	})
	return r
}

// BenchmarkRegistry_Build measures the registry-lookup + factory-invoke
// cost paid every time a worker materialises a task from a job graph.
// Sub-benched per operator kind because the lookup map differs.
func BenchmarkRegistry_Build(b *testing.B) {
	r := newBenchRegistry()
	tc := benchTC()
	ctx := context.Background()

	cases := []struct {
		name string
		desc rpc.OperatorDescriptor
	}{
		{"Source", rpc.OperatorDescriptor{OperatorID: "1", Type: rpc.OperatorTypeSource, ClassName: "src"}},
		{"Map", rpc.OperatorDescriptor{OperatorID: "2", Type: rpc.OperatorTypeMap, ClassName: "m"}},
		{"FlatMap", rpc.OperatorDescriptor{OperatorID: "3", Type: rpc.OperatorTypeFlatMap, ClassName: "fm"}},
		{"Sink", rpc.OperatorDescriptor{OperatorID: "4", Type: rpc.OperatorTypeSink, ClassName: "snk"}},
	}

	for _, tc2 := range cases {
		b.Run(tc2.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := r.Build(ctx, tc2.desc, tc); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
