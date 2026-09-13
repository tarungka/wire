package worker

import (
	"context"
	"fmt"

	"github.com/tarungka/wire/internal/engine"
)

// KeySelector computes a partitioning key without replacing the event payload.
type KeySelector func(context.Context, engine.Event) ([]byte, error)
type KeySelectorFactory func(context.Context, []byte, TaskContext) (KeySelector, error)

// RegisterKeyBy registers a per-task key selector factory.
func (r *Registry) RegisterKeyBy(name string, factory KeySelectorFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keySelectors == nil {
		r.keySelectors = make(map[string]KeySelectorFactory)
	}
	if _, exists := r.keySelectors[name]; exists {
		panic(fmt.Sprintf("worker: key selector %q already registered", name))
	}
	r.keySelectors[name] = factory
}
func RegisterKeyBy(name string, factory KeySelectorFactory) {
	defaultRegistry.RegisterKeyBy(name, factory)
}

type keyByOperator struct{ selectKey KeySelector }

func (*keyByOperator) Open(context.Context) error        { return nil }
func (*keyByOperator) Close() error                      { return nil }
func (*keyByOperator) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (op *keyByOperator) Map(ctx context.Context, event engine.Event) (engine.Event, error) {
	key, err := op.selectKey(ctx, event)
	if err != nil {
		return engine.Event{}, err
	}
	event.Key = append([]byte(nil), key...)
	return event, nil
}
