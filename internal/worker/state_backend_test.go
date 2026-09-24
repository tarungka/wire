package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestInvalidStateBackendRejectedBeforeFactory(t *testing.T) {
	registry := NewRegistry()
	called := false
	registry.RegisterProcess("process", func(context.Context, []byte, TaskContext) (engine.FlatMapOperator, error) {
		called = true
		return nil, nil
	})
	desc := rpc.TaskDescriptor{TaskID: "task", OperatorChain: []rpc.OperatorDescriptor{{OperatorID: "process", Type: rpc.OperatorTypeProcess, ClassName: "process", StateBackend: &rpc.StateBackendSpec{Type: "hashmap", MaxMemoryBytes: -1}}}}
	err := newTaskExecutor(registry).run(t.Context(), "job", "task", desc, zerolog.Nop(), nil)
	if err == nil || !strings.Contains(err.Error(), "nonnegative") || called {
		t.Fatalf("invalid configuration reached factory: called=%t err=%v", called, err)
	}
}
