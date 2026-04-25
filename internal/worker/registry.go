package worker

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// TaskContext is passed to an operator factory when a task is deployed.
// It carries per-task identity and scheduling information.
type TaskContext struct {
	TaskID       string
	JobID        string
	OperatorID   string
	SubtaskIndex int32
	Parallelism  int32
	KeyGroup     rpc.KeyGroupRange
	Log          zerolog.Logger
}

// Factory types. Each factory receives the operator's config bytes (verbatim
// from OperatorDescriptor.Config) and a TaskContext, and returns a live
// engine operator ready to be opened and run.
type (
	SourceFactory  func(ctx context.Context, cfg []byte, tc TaskContext) (engine.SourceOperator, error)
	MapFactory     func(ctx context.Context, cfg []byte, tc TaskContext) (engine.MapOperator, error)
	FlatMapFactory func(ctx context.Context, cfg []byte, tc TaskContext) (engine.FlatMapOperator, error)
	SinkFactory    func(ctx context.Context, cfg []byte, tc TaskContext) (engine.SinkOperator, error)
)

// Registry maps operator ClassName strings to factory functions. A worker
// looks up factories by the ClassName in each OperatorDescriptor at task
// deploy time.
type Registry struct {
	mu       sync.RWMutex
	sources  map[string]SourceFactory
	maps     map[string]MapFactory
	flatMaps map[string]FlatMapFactory
	sinks    map[string]SinkFactory
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		sources:  make(map[string]SourceFactory),
		maps:     make(map[string]MapFactory),
		flatMaps: make(map[string]FlatMapFactory),
		sinks:    make(map[string]SinkFactory),
	}
}

// RegisterSource adds a source factory. Panics on duplicate name to surface
// configuration mistakes at startup.
func (r *Registry) RegisterSource(name string, f SourceFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sources[name]; exists {
		panic(fmt.Sprintf("worker: source %q already registered", name))
	}
	r.sources[name] = f
}

// RegisterMap adds a map factory. Panics on duplicate name.
func (r *Registry) RegisterMap(name string, f MapFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.maps[name]; exists {
		panic(fmt.Sprintf("worker: map %q already registered", name))
	}
	r.maps[name] = f
}

// RegisterFlatMap adds a flat-map factory. Panics on duplicate name.
func (r *Registry) RegisterFlatMap(name string, f FlatMapFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.flatMaps[name]; exists {
		panic(fmt.Sprintf("worker: flatmap %q already registered", name))
	}
	r.flatMaps[name] = f
}

// RegisterSink adds a sink factory. Panics on duplicate name.
func (r *Registry) RegisterSink(name string, f SinkFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sinks[name]; exists {
		panic(fmt.Sprintf("worker: sink %q already registered", name))
	}
	r.sinks[name] = f
}

// Build resolves an OperatorDescriptor to a concrete engine.Operator by
// looking up the factory keyed by desc.ClassName. Filter operators are
// handled by the "filter" factory type on maps (a filter is a map that
// conditionally returns a zero event).
func (r *Registry) Build(ctx context.Context, desc rpc.OperatorDescriptor, tc TaskContext) (engine.Operator, error) {
	if desc.ClassName == "" {
		return nil, fmt.Errorf("worker: operator %q has no ClassName (required for cluster mode)", desc.OperatorID)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	switch desc.Type {
	case rpc.OperatorTypeSource:
		f, ok := r.sources[desc.ClassName]
		if !ok {
			return nil, fmt.Errorf("worker: unknown source %q (did you forget to register it?)", desc.ClassName)
		}
		op, err := f(ctx, desc.Config, tc)
		if err != nil {
			return nil, fmt.Errorf("worker: source %q factory: %w", desc.ClassName, err)
		}
		return op, nil

	case rpc.OperatorTypeMap, rpc.OperatorTypeFilter:
		f, ok := r.maps[desc.ClassName]
		if !ok {
			return nil, fmt.Errorf("worker: unknown map/filter %q", desc.ClassName)
		}
		op, err := f(ctx, desc.Config, tc)
		if err != nil {
			return nil, fmt.Errorf("worker: map %q factory: %w", desc.ClassName, err)
		}
		return op, nil

	case rpc.OperatorTypeFlatMap:
		f, ok := r.flatMaps[desc.ClassName]
		if !ok {
			return nil, fmt.Errorf("worker: unknown flatmap %q", desc.ClassName)
		}
		op, err := f(ctx, desc.Config, tc)
		if err != nil {
			return nil, fmt.Errorf("worker: flatmap %q factory: %w", desc.ClassName, err)
		}
		return op, nil

	case rpc.OperatorTypeSink:
		f, ok := r.sinks[desc.ClassName]
		if !ok {
			return nil, fmt.Errorf("worker: unknown sink %q", desc.ClassName)
		}
		op, err := f(ctx, desc.Config, tc)
		if err != nil {
			return nil, fmt.Errorf("worker: sink %q factory: %w", desc.ClassName, err)
		}
		return op, nil

	default:
		return nil, fmt.Errorf("worker: operator type %s is not supported in Phase 1", desc.Type)
	}
}

// defaultRegistry is the package-level registry used by convenience functions.
// User code typically calls worker.RegisterSource/RegisterMap/... in init or
// main before constructing a Worker.
var defaultRegistry = NewRegistry()

// DefaultRegistry returns the package-level registry.
func DefaultRegistry() *Registry { return defaultRegistry }

// RegisterSource registers a source factory in the default registry.
func RegisterSource(name string, f SourceFactory) { defaultRegistry.RegisterSource(name, f) }

// RegisterMap registers a map factory in the default registry.
func RegisterMap(name string, f MapFactory) { defaultRegistry.RegisterMap(name, f) }

// RegisterFlatMap registers a flat-map factory in the default registry.
func RegisterFlatMap(name string, f FlatMapFactory) { defaultRegistry.RegisterFlatMap(name, f) }

// RegisterSink registers a sink factory in the default registry.
func RegisterSink(name string, f SinkFactory) { defaultRegistry.RegisterSink(name, f) }
