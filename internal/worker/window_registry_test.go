package worker

import (
	"context"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

type registeredWindowProbe struct{}

func (*registeredWindowProbe) Open(context.Context) error        { return nil }
func (*registeredWindowProbe) Close() error                      { return nil }
func (*registeredWindowProbe) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (*registeredWindowProbe) RestoreCheckpoint([]byte) error    { return nil }
func (*registeredWindowProbe) FlatMap(context.Context, engine.Event, func(engine.Event)) error {
	return nil
}
func (*registeredWindowProbe) OnWatermark(context.Context, int64) ([]engine.Event, error) {
	return nil, nil
}

func TestWindowFactoryDeploymentContract(t *testing.T) {
	registry := NewRegistry()
	want := &registeredWindowProbe{}
	registry.RegisterWindow("window", func(_ context.Context, config []byte, task TaskContext) (WindowOperator, error) {
		if string(config) != "config" || task.TaskID != "task" {
			t.Fatal("factory lost deployment context")
		}
		return want, nil
	})
	got, err := registry.Build(context.Background(), rpc.OperatorDescriptor{Type: rpc.OperatorTypeWindow, ClassName: "window", Config: []byte("config")}, TaskContext{TaskID: "task"})
	if err != nil || got != want {
		t.Fatalf("factory result: %v %v", got, err)
	}
	if _, err := registry.Build(context.Background(), rpc.OperatorDescriptor{Type: rpc.OperatorTypeWindow, ClassName: "missing"}, TaskContext{}); err == nil {
		t.Fatal("unknown window accepted")
	}
}
