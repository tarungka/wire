package engine

import (
	"context"
	"strings"
	"testing"
)

func TestTransactionalSinkRejectsRecordRecoveryBeforeProcessing(t *testing.T) {
	for _, cfg := range []ErrorHandlerConfig{{MaxRetries: 1}, {OnExhausted: DropEvent}, {OnExhausted: RouteToDLQ}} {
		sink := &mockTransactionalSink{}
		config := DefaultTaskSlotConfig()
		config.ErrorConfigs = []ErrorHandlerConfig{cfg}
		slot := NewTaskSlot(config, nil, nil, []Operator{sink}, nil)
		err := slot.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "transactional sink") {
			t.Fatalf("got %v", err)
		}
		if sink.BeginTxnCalls() != 0 || len(sink.written) != 0 {
			t.Fatal("unsafe policy reached transaction")
		}
	}
	// A policy on an upstream transform must not forbid transactional sinks.
	if err := ValidateTransactionalErrorPolicies([]Operator{&noopMap{}, &mockTransactionalSink{}}, []ErrorHandlerConfig{{MaxRetries: 1}, {}}); err != nil {
		t.Fatal(err)
	}
}
