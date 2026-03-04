package sdk

import (
	"context"
	"testing"
)

func TestMiniClusterBasic(t *testing.T) {
	mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 2})
	defer mc.Shutdown()

	env := mc.GetExecutionEnvironment()
	sink := &collectSink{}

	env.AddSource(&sliceSource{
		events: []Event{
			{Value: []byte("a")},
			{Value: []byte("b")},
			{Value: []byte("c")},
		},
	}).
		Map(func(e Event) (Event, error) { return e, nil }).
		AddSink(sink)

	_, err := env.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	events := sink.Events()
	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}
}

func TestMiniClusterDefaultSlots(t *testing.T) {
	mc := NewMiniCluster(MiniClusterConfig{})
	if mc.config.NumTaskSlots != 1 {
		t.Errorf("expected default NumTaskSlots=1, got %d", mc.config.NumTaskSlots)
	}
}

func TestMiniClusterShutdown(t *testing.T) {
	mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
	if err := mc.Shutdown(); err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}
