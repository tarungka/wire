package worker

import (
	"context"
	"fmt"

	"github.com/tarungka/wire/internal/engine"
)

// WindowOperator consumes records and ordered watermarks, and can restore its
// checkpointed accumulators and firing state before processing resumes.
type WindowOperator interface {
	engine.FlatMapOperator
	engine.WatermarkOperator
	engine.CheckpointRestorer
}

type WindowFactory func(context.Context, []byte, TaskContext) (WindowOperator, error)

func (r *Registry) RegisterWindow(name string, factory WindowFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.windows == nil {
		r.windows = make(map[string]WindowFactory)
	}
	if _, exists := r.windows[name]; exists {
		panic(fmt.Sprintf("worker: window %q already registered", name))
	}
	r.windows[name] = factory
}

func RegisterWindow(name string, factory WindowFactory) {
	defaultRegistry.RegisterWindow(name, factory)
}
