package sdk

import (
	"context"
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/worker"
)

// WorkerTaskContext identifies a deployed operator instance. Factories run for
// every attempt and must return fresh, unopened connectors or function closures.
type WorkerTaskContext struct {
	JobID, TaskID, OperatorID, AttemptID string
	Index, Parallelism, NumKeyGroups     int
	EpochID, DeploymentGeneration        uint64
}

func publicTaskContext(tc worker.TaskContext) WorkerTaskContext {
	return WorkerTaskContext{JobID: tc.JobID, TaskID: tc.TaskID, OperatorID: tc.OperatorID, AttemptID: tc.AttemptID, Index: int(tc.SubtaskIndex), Parallelism: int(tc.Parallelism), NumKeyGroups: tc.NumKeyGroups, EpochID: tc.EpochID, DeploymentGeneration: tc.DeploymentGeneration}
}

// WorkerRegistry contains the named factories available to RunWorker. Register
// all classes before starting workers. Duplicate names panic to expose mistakes.
type WorkerRegistry struct{ registry *worker.Registry }

func NewWorkerRegistry() *WorkerRegistry { return &WorkerRegistry{registry: worker.NewRegistry()} }

type WorkerSourceFactory func(context.Context, []byte, WorkerTaskContext) (Source, error)
type WorkerSinkFactory func(context.Context, []byte, WorkerTaskContext) (Sink, error)
type WorkerMapFactory func(context.Context, []byte, WorkerTaskContext) (MapFunc, error)
type WorkerFlatMapFactory func(context.Context, []byte, WorkerTaskContext) (FlatMapFunc, error)
type WorkerFilterFactory func(context.Context, []byte, WorkerTaskContext) (FilterFunc, error)
type WorkerKeyFactory func(context.Context, []byte, WorkerTaskContext) (KeySelector, error)
type WorkerProcessFactory func(context.Context, []byte, WorkerTaskContext) (ProcessDefinition, error)
type WorkerWindowFactory func(context.Context, []byte, WorkerTaskContext) (WindowDefinition, error)

type ProcessDefinition struct {
	Process      ProcessFunc
	OnTimer      TimerFunc
	StateBackend StateBackendConfig
}

// WindowDefinition supplies the aggregation behavior. Dimensions and lateness
// come from the submitted Window(...).ApplyNamed graph. Set exactly one aggregation form.
type WindowDefinition struct {
	Aggregator   Aggregator
	Reduce       ReduceFunc
	Apply        WindowFunc
	StateBackend StateBackendConfig
}

func (r *WorkerRegistry) RegisterSource(name string, factory WorkerSourceFactory) {
	if factory == nil {
		panic("sdk: nil source factory")
	}
	r.registry.RegisterSource(name, func(ctx context.Context, config []byte, tc worker.TaskContext) (engine.SourceOperator, error) {
		source, err := factory(ctx, config, publicTaskContext(tc))
		if err != nil {
			return nil, err
		}
		if nilConnector(source) {
			return nil, fmt.Errorf("sdk: source factory %q returned nil", name)
		}
		return &sourceAdapter{source: source}, nil
	})
}
func (r *WorkerRegistry) RegisterSink(name string, factory WorkerSinkFactory) {
	if factory == nil {
		panic("sdk: nil sink factory")
	}
	r.registry.RegisterSink(name, func(ctx context.Context, config []byte, tc worker.TaskContext) (engine.SinkOperator, error) {
		sink, err := factory(ctx, config, publicTaskContext(tc))
		if err != nil {
			return nil, err
		}
		if nilConnector(sink) {
			return nil, fmt.Errorf("sdk: sink factory %q returned nil", name)
		}
		return adaptSink(sink), nil
	})
}
func (r *WorkerRegistry) RegisterMap(name string, factory WorkerMapFactory) {
	if factory == nil {
		panic("sdk: nil map factory")
	}
	r.registry.RegisterMap(name, func(ctx context.Context, config []byte, tc worker.TaskContext) (engine.MapOperator, error) {
		fn, err := factory(ctx, config, publicTaskContext(tc))
		if err != nil {
			return nil, err
		}
		if fn == nil {
			return nil, fmt.Errorf("sdk: map factory %q returned nil", name)
		}
		return &mapAdapter{fn: fn}, nil
	})
}
func (r *WorkerRegistry) RegisterFlatMap(name string, factory WorkerFlatMapFactory) {
	if factory == nil {
		panic("sdk: nil flat-map factory")
	}
	r.registry.RegisterFlatMap(name, func(ctx context.Context, config []byte, tc worker.TaskContext) (engine.FlatMapOperator, error) {
		fn, err := factory(ctx, config, publicTaskContext(tc))
		if err != nil {
			return nil, err
		}
		if fn == nil {
			return nil, fmt.Errorf("sdk: flat-map factory %q returned nil", name)
		}
		return &flatMapAdapter{fn: fn}, nil
	})
}
func (r *WorkerRegistry) RegisterFilter(name string, factory WorkerFilterFactory) {
	if factory == nil {
		panic("sdk: nil filter factory")
	}
	r.registry.RegisterMap(name, func(ctx context.Context, config []byte, tc worker.TaskContext) (engine.MapOperator, error) {
		fn, err := factory(ctx, config, publicTaskContext(tc))
		if err != nil {
			return nil, err
		}
		if fn == nil {
			return nil, fmt.Errorf("sdk: filter factory %q returned nil", name)
		}
		return &filterAdapter{fn: fn}, nil
	})
}
func (r *WorkerRegistry) RegisterKeyBy(name string, factory WorkerKeyFactory) {
	if factory == nil {
		panic("sdk: nil key selector factory")
	}
	r.registry.RegisterKeyBy(name, func(ctx context.Context, config []byte, tc worker.TaskContext) (worker.KeySelector, error) {
		fn, err := factory(ctx, config, publicTaskContext(tc))
		if err != nil {
			return nil, err
		}
		if fn == nil {
			return nil, fmt.Errorf("sdk: key selector factory %q returned nil", name)
		}
		return func(_ context.Context, e engine.Event) ([]byte, error) { return fn(e) }, nil
	})
}
func (r *WorkerRegistry) RegisterProcess(name string, factory WorkerProcessFactory) {
	if factory == nil {
		panic("sdk: nil Process factory")
	}
	r.registry.RegisterProcess(name, func(ctx context.Context, config []byte, tc worker.TaskContext) (engine.FlatMapOperator, error) {
		definition, err := factory(ctx, config, publicTaskContext(tc))
		if err != nil {
			return nil, err
		}
		if definition.Process == nil {
			return nil, fmt.Errorf("sdk: Process factory %q returned nil", name)
		}
		if err := definition.StateBackend.validate(); err != nil {
			return nil, err
		}
		op := NewProcessOperator(definition.Process, definition.OnTimer, definition.StateBackend)
		op.SetStateBackendFactory(workerBackendFactory(definition.StateBackend, tc))
		return op, nil
	})
}
func (r *WorkerRegistry) RegisterWindow(name string, factory WorkerWindowFactory) {
	if factory == nil {
		panic("sdk: nil window factory")
	}
	r.registry.RegisterWindow(name, func(ctx context.Context, config []byte, tc worker.TaskContext) (worker.WindowOperator, error) {
		definition, err := factory(ctx, config, publicTaskContext(tc))
		if err != nil {
			return nil, err
		}
		forms := 0
		if definition.Aggregator != nil {
			forms++
		}
		if definition.Reduce != nil {
			forms++
		}
		if definition.Apply != nil {
			forms++
		}
		if forms != 1 {
			return nil, fmt.Errorf("sdk: window %q requires exactly one aggregation form", name)
		}
		if err := definition.StateBackend.validate(); err != nil {
			return nil, err
		}
		// The worker replaces these valid initial dimensions with the graph's
		// required window definition before Open.
		op, err := embeddedWindow(&StreamNode{Window: TumblingWindow(time.Millisecond), Aggregator: definition.Aggregator, ReduceFn: definition.Reduce, WindowFn: definition.Apply})
		if err != nil {
			return nil, err
		}
		window := op.(*engine.EventTimeWindowOperator)
		window.SetStateBackendFactory(workerBackendFactory(definition.StateBackend, tc))
		return window, nil
	})
}

func workerBackendFactory(c StateBackendConfig, tc worker.TaskContext) func() (engine.StateBackend, func(), error) {
	return engine.ScopedStateBackendFactory(engine.StateBackendConfig{Type: engine.StateBackendType(c.Type), HashMapMemLimit: int64(c.MaxMemoryMB) * 1024 * 1024, PebbleDataDir: c.DataDir, PebbleMaxCompactionConcurrency: c.MaxCompactionConcurrency}, tc.JobID, tc.OperatorID, tc.AttemptID, int(tc.SubtaskIndex))
}
