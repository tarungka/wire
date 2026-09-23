package sdk

import (
	"context"
	"strings"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/worker"
)

type sdkTransactionProbe struct {
	opened   bool
	prepared bool
	restored string
	commits  int
}

func (s *sdkTransactionProbe) Open(context.Context) error              { s.opened = true; return nil }
func (*sdkTransactionProbe) Close() error                              { return nil }
func (*sdkTransactionProbe) Write(context.Context, Event) error        { return nil }
func (*sdkTransactionProbe) BeginTransaction(context.Context) error    { return nil }
func (s *sdkTransactionProbe) PreCommit(context.Context, uint64) error { s.prepared = true; return nil }
func (s *sdkTransactionProbe) Commit(context.Context, uint64) error    { s.commits++; return nil }
func (*sdkTransactionProbe) Abort(context.Context) error               { return nil }
func (*sdkTransactionProbe) Checkpoint(uint64) ([]byte, error) {
	return []byte("prepared-external-handle"), nil
}
func (s *sdkTransactionProbe) RestoreCheckpoint(data []byte) error {
	s.restored = string(data)
	return nil
}

var _ TransactionalSink = (*sdkTransactionProbe)(nil)

func TestSinkAdapterPreservesTransactionAndRestore(t *testing.T) {
	probe := &sdkTransactionProbe{}
	adapted := adaptSink(probe)
	txn, ok := adapted.(engine.TransactionalSink)
	if !ok {
		t.Fatal("adapter hid transactional capability")
	}
	if err := txn.PreCommit(t.Context(), 7); err != nil {
		t.Fatal(err)
	}
	state, err := adapted.Checkpoint(7)
	if err != nil {
		t.Fatal(err)
	}
	restorer, ok := adapted.(engine.CheckpointRestorer)
	if !ok {
		t.Fatal("adapter hid transaction recovery")
	}
	if err := restorer.RestoreCheckpoint(state); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(t.Context(), 7); err != nil {
		t.Fatal(err)
	}
	if !probe.prepared || probe.restored != "prepared-external-handle" || probe.commits != 1 {
		t.Fatalf("hooks were not forwarded: %+v", probe)
	}
	if _, ok := adaptSink(&collectSink{}).(engine.TransactionalSink); ok {
		t.Fatal("ordinary sink acquired transaction semantics")
	}
}

func TestSDKTransactionalSinkFitsWorkerFactory(t *testing.T) {
	var factory worker.SinkFactory = func(context.Context, []byte, worker.TaskContext) (engine.SinkOperator, error) {
		var sink TransactionalSink = &sdkTransactionProbe{}
		return sink, nil
	}
	sink, err := factory(t.Context(), nil, worker.TaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sink.(engine.TransactionalSink); !ok {
		t.Fatal("worker factory erased transaction contract")
	}
}

func TestEmbeddedTransactionRequiresDurableCheckpointRuntime(t *testing.T) {
	env := New()
	sink := &sdkTransactionProbe{}
	env.AddSource(&sliceSource{events: []Event{{Value: []byte("record")}}}).AddSink(sink)
	_, err := env.Execute(t.Context())
	if err == nil || !strings.Contains(err.Error(), "durable global checkpoint decisions") {
		t.Fatalf("transaction executed without commit authority: %v", err)
	}
	if sink.opened {
		t.Fatal("unsupported runtime opened external transaction resources")
	}
}

func (*sdkTransactionProbe) RecoverTransactions(context.Context, TransactionRecovery) error {
	return nil
}
