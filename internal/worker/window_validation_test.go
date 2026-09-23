package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestWindowDeploymentValidationBeforeFactory(t *testing.T) {
	for _, scenario := range []string{"dimensions", "policy", "type"} {
		t.Run(scenario, func(t *testing.T) {
			registry := NewRegistry()
			registry.RegisterSource("source", func(context.Context, []byte, TaskContext) (engine.SourceOperator, error) {
				t.Fatal("called factory before validation")
				return nil, nil
			})
			window := rpc.OperatorDescriptor{OperatorID: "window", ClassName: "window", Type: rpc.OperatorTypeWindow, Window: &rpc.WindowDefinition{Kind: "tumbling", Size: 10}}
			switch scenario {
			case "dimensions":
				window.Window.Size = 0
			case "policy":
				window.ErrorPolicy = &rpc.ErrorPolicy{OnExhausted: "drop"}
			case "type":
				window.Type = rpc.OperatorTypeMap
			}
			err := newTaskExecutor(registry).run(context.Background(), "job", "task", rpc.TaskDescriptor{OperatorChain: []rpc.OperatorDescriptor{{OperatorID: "source", ClassName: "source", Type: rpc.OperatorTypeSource}, window}}, zerolog.Nop(), nil)
			if err == nil || !strings.Contains(err.Error(), "window") {
				t.Fatalf("expected window validation error, got %v", err)
			}
		})
	}
}
