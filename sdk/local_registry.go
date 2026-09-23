package sdk

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

// localRegistry translates closures into factories without serializing Go
// function values. Only the resulting graph crosses the real worker RPCs.
func (env *StreamExecutionEnvironment) localRegistry(ctx context.Context) (rpc.JobGraph, *worker.Registry, func(), error) {
	graph := newStreamGraph()
	graph.edges = append([]StreamEdge(nil), env.graph.edges...)
	reg := worker.NewRegistry()
	var destinations []*engine.DLQDestination
	sharedDestinations := make(map[*sharedPipelineDLQSink]*localDLQSink)
	cleanup := func() {
		for _, d := range destinations {
			d.Close()
		}
	}
	fail := func(err error) (rpc.JobGraph, *worker.Registry, func(), error) {
		cleanup()
		return rpc.JobGraph{}, nil, nil, err
	}
	for _, original := range env.graph.topoSort() {
		node := *original
		if node.NamedDLQ != nil {
			return fail(fmt.Errorf("sdk: local execution requires a concrete DLQ sink"))
		}
		if node.Source != nil || node.Sink != nil {
			if node.Parallelism > 1 {
				return fail(fmt.Errorf("sdk: parallel connectors require an instance factory"))
			}
			node.Parallelism = 1
		}
		if node.Type == NodeReduce {
			node.Type = NodeWindow
		}
		node.ClassName = fmt.Sprintf("sdk-local-%d", node.ID)
		graph.nodes[node.ID] = &node
		if node.DLQSink != nil {
			var shared *localDLQSink
			pipeline, isPipeline := node.DLQSink.(*sharedPipelineDLQSink)
			if isPipeline {
				shared = sharedDestinations[pipeline]
			}
			if shared == nil {
				destination, err := engine.OpenDLQDestination(ctx, node.DLQSink, zerolog.Nop())
				if err != nil {
					return fail(err)
				}
				destinations = append(destinations, destination)
				shared = &localDLQSink{sink: node.DLQSink}
				if isPipeline {
					sharedDestinations[pipeline] = shared
				}
			}
			name := node.ClassName + "-dlq"
			reg.RegisterSink(name, func(context.Context, []byte, worker.TaskContext) (engine.SinkOperator, error) { return shared, nil })
			node.NamedDLQ = &rpc.DLQSinkDescriptor{ClassName: name}
		}
		switch node.Type {
		case NodeSource:
			reg.RegisterSource(node.ClassName, func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.SourceOperator, error) {
				source := node.Source
				if node.SourceFactory != nil {
					var err error
					source, err = node.SourceFactory(InstanceContext{Index: int(tc.SubtaskIndex), Parallelism: int(tc.Parallelism)})
					if err != nil {
						return nil, err
					}
				}
				if nilConnector(source) {
					return nil, fmt.Errorf("sdk: source factory returned nil")
				}
				return &sourceAdapter{source: source, timestamp: node.TimestampExtractor}, nil
			})
		case NodeSink:
			reg.RegisterSink(node.ClassName, func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.SinkOperator, error) {
				sink := node.Sink
				if node.SinkFactory != nil {
					var err error
					sink, err = node.SinkFactory(InstanceContext{Index: int(tc.SubtaskIndex), Parallelism: int(tc.Parallelism)})
					if err != nil {
						return nil, err
					}
				}
				if nilConnector(sink) {
					return nil, fmt.Errorf("sdk: sink factory returned nil")
				}
				return adaptSink(sink), nil
			})
		case NodeMap:
			reg.RegisterMap(node.ClassName, func(context.Context, []byte, worker.TaskContext) (engine.MapOperator, error) {
				return &mapAdapter{fn: node.MapFn}, nil
			})
		case NodeFilter:
			reg.RegisterMap(node.ClassName, func(context.Context, []byte, worker.TaskContext) (engine.MapOperator, error) {
				return &filterAdapter{fn: node.FilterFn}, nil
			})
		case NodeFlatMap:
			reg.RegisterFlatMap(node.ClassName, func(context.Context, []byte, worker.TaskContext) (engine.FlatMapOperator, error) {
				return &flatMapAdapter{fn: node.FlatMapFn}, nil
			})
		case NodeKeyBy:
			reg.RegisterKeyBy(node.ClassName, func(context.Context, []byte, worker.TaskContext) (worker.KeySelector, error) {
				return func(_ context.Context, e engine.Event) ([]byte, error) { return node.KeyByFn(e) }, nil
			})
		case NodeProcess:
			reg.RegisterProcess(node.ClassName, func(context.Context, []byte, worker.TaskContext) (engine.FlatMapOperator, error) {
				return NewProcessOperator(node.ProcessFn, node.TimerFn, StateBackendConfig{}), nil
			})
		case NodeWindow, NodeReduce:
			reg.RegisterWindow(node.ClassName, func(_ context.Context, _ []byte, _ worker.TaskContext) (worker.WindowOperator, error) {
				op, err := embeddedWindow(&node)
				if err != nil {
					return nil, err
				}
				window := op.(*engine.EventTimeWindowOperator)

				return window, nil
			})
		default:
			return fail(fmt.Errorf("sdk: unsupported local node %d", node.Type))
		}
	}
	result := graph.toJobGraph(env.parallelism)
	result.NumKeyGroups = env.numKeyGroups
	env.configureGraphStateBackend(&result)
	result.CheckpointPolicy = env.checkpointPolicy()
	var err error
	result.RestartPolicy, err = env.restartPolicy()
	if err != nil {
		return fail(err)
	}
	return result, reg, cleanup, nil
}

// Worker wrappers borrow this destination. The execution owner handles its
// lifecycle after every worker has stopped; concurrent partitions serialize writes.
type localDLQSink struct {
	sink Sink
	mu   sync.Mutex
}

func (*localDLQSink) Open(context.Context) error        { return nil }
func (*localDLQSink) Close() error                      { return nil }
func (*localDLQSink) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (s *localDLQSink) Write(ctx context.Context, e engine.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.Write(ctx, e)
}
