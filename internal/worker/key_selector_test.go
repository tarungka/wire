package worker

import (
	"context"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestRegisteredKeyByOwnsSelectedKey(t *testing.T) {
	registry := NewRegistry()
	key := []byte("selected")
	registry.RegisterKeyBy("select", func(context.Context, []byte, TaskContext) (KeySelector, error) {
		return func(context.Context, engine.Event) ([]byte, error) { return key, nil }, nil
	})
	op, err := registry.Build(context.Background(), rpc.OperatorDescriptor{Type: rpc.OperatorTypeKeyBy, ClassName: "select"}, TaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := op.(engine.MapOperator).Map(context.Background(), engine.Event{Value: []byte("payload"), EventTime: 123})
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 'X'
	if string(got.Key) != "selected" || string(got.Value) != "payload" || got.EventTime != 123 {
		t.Fatalf("event=%+v", got)
	}
}
