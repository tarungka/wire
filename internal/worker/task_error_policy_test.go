package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

type policySource struct {
	stubSource
	done bool
}

func (s *policySource) ReadBatch(context.Context) ([]engine.Event, error) {
	if s.done {
		return nil, nil
	}
	s.done = true
	return []engine.Event{{Value: []byte("bad")}}, nil
}

type policyMap struct{ stubMap }

func (*policyMap) Map(context.Context, engine.Event) (engine.Event, error) {
	return engine.Event{}, errors.New("invalid")
}

type policySink struct {
	stubSink
	opened, closed bool
	events         []engine.Event
}

func (s *policySink) Open(context.Context) error { s.opened = true; return nil }
func (s *policySink) Close() error               { s.closed = true; return nil }
func (s *policySink) Write(_ context.Context, e engine.Event) error {
	if !s.opened || s.closed {
		return errors.New("invalid lifecycle")
	}
	s.events = append(s.events, e)
	return nil
}
func TestTaskExecutorNamedDLQ(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	defer func() { otel.SetMeterProvider(previous); _ = provider.Shutdown(context.Background()) }()
	reg := NewRegistry()
	dlq := &policySink{}
	reg.RegisterSource("source", func(context.Context, []byte, TaskContext) (engine.SourceOperator, error) { return &policySource{}, nil })
	reg.RegisterMap("parse", func(context.Context, []byte, TaskContext) (engine.MapOperator, error) { return &policyMap{}, nil })
	reg.RegisterSink("dlq", func(context.Context, []byte, TaskContext) (engine.SinkOperator, error) { return dlq, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := newTaskExecutor(reg).run(ctx, "job", "task", rpc.TaskDescriptor{OperatorChain: []rpc.OperatorDescriptor{
		{OperatorID: "source", Type: rpc.OperatorTypeSource, ClassName: "source"},
		{OperatorID: "parse", Type: rpc.OperatorTypeMap, ClassName: "parse", ErrorPolicy: &rpc.ErrorPolicy{OnExhausted: "dlq"}, DLQSink: &rpc.DLQSinkDescriptor{ClassName: "dlq"}},
	}}, zerolog.Nop(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !dlq.opened || !dlq.closed || len(dlq.events) != 1 {
		t.Fatalf("DLQ lifecycle/output: %+v", dlq)
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "wire_operator_errors_total" && metric.Name != "wire_dlq_events_total" {
				continue
			}
			for _, point := range metric.Data.(metricdata.Sum[int64]).DataPoints {
				op, _ := point.Attributes.Value("operator")
				task, _ := point.Attributes.Value("task_id")
				if op.AsString() != "parse" || task.AsString() != "task" {
					t.Fatalf("wrong attribution: %+v", point)
				}
				counts[metric.Name] += point.Value
			}
		}
	}
	if counts["wire_operator_errors_total"] != 1 || counts["wire_dlq_events_total"] != 1 {
		t.Fatalf("missing runtime counters: %+v", counts)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(dlq.events[0].Value, &record); err != nil {
		t.Fatal(err)
	}
	if string(record["operator"]) != "\"parse\"" {
		t.Fatalf("wrong operator: %s", record["operator"])
	}
}

type startupFailureDLQ struct {
	policySink
	panics bool
}

func (s *startupFailureDLQ) Open(context.Context) error {
	s.opened = true
	if s.panics {
		panic("DLQ unavailable")
	}
	return errors.New("DLQ unavailable")
}

type transactionalPolicySink struct{ policySink }

func (*transactionalPolicySink) BeginTransaction(context.Context) error  { return nil }
func (*transactionalPolicySink) PreCommit(context.Context, uint64) error { return nil }
func (*transactionalPolicySink) Commit(context.Context, uint64) error    { return nil }
func (*transactionalPolicySink) Abort(context.Context) error             { return nil }

func TestTaskExecutorDLQStartupFailsBeforeRunning(t *testing.T) {
	for _, panics := range []bool{false, true} {
		dlq := &startupFailureDLQ{panics: panics}
		reg := NewRegistry()
		reg.RegisterSource("source", func(context.Context, []byte, TaskContext) (engine.SourceOperator, error) { return &policySource{}, nil })
		reg.RegisterMap("parse", func(context.Context, []byte, TaskContext) (engine.MapOperator, error) { return &policyMap{}, nil })
		reg.RegisterSink("dlq", func(context.Context, []byte, TaskContext) (engine.SinkOperator, error) { return dlq, nil })
		running := false
		err := newTaskExecutor(reg).run(context.Background(), "job", "task", rpc.TaskDescriptor{OperatorChain: []rpc.OperatorDescriptor{
			{OperatorID: "source", Type: rpc.OperatorTypeSource, ClassName: "source"},
			{OperatorID: "parse", Type: rpc.OperatorTypeMap, ClassName: "parse", ErrorPolicy: &rpc.ErrorPolicy{OnExhausted: "dlq"}, DLQSink: &rpc.DLQSinkDescriptor{ClassName: "dlq"}},
		}}, zerolog.Nop(), func() { running = true })
		if err == nil || running || !dlq.opened || !dlq.closed || len(dlq.events) != 0 {
			t.Fatalf("err=%v running=%t sink=%+v", err, running, dlq)
		}
	}
}

func TestTaskExecutorMissingDLQRejectedBeforeFactory(t *testing.T) {
	reg := NewRegistry()
	called := false
	reg.RegisterSource("source", func(context.Context, []byte, TaskContext) (engine.SourceOperator, error) {
		called = true
		return &policySource{}, nil
	})
	err := newTaskExecutor(reg).run(context.Background(), "job", "task", rpc.TaskDescriptor{OperatorChain: []rpc.OperatorDescriptor{
		{OperatorID: "source", Type: rpc.OperatorTypeSource, ClassName: "source"},
		{OperatorID: "parse", Type: rpc.OperatorTypeMap, ClassName: "parse", ErrorPolicy: &rpc.ErrorPolicy{OnExhausted: "dlq"}},
	}}, zerolog.Nop(), nil)
	if err == nil || called {
		t.Fatalf("err=%v factory called=%t", err, called)
	}
}

func TestTaskExecutorRejectsTransactionalDLQ(t *testing.T) {
	reg := NewRegistry()
	dlq := &transactionalPolicySink{}
	reg.RegisterSource("source", func(context.Context, []byte, TaskContext) (engine.SourceOperator, error) { return &policySource{}, nil })
	reg.RegisterMap("parse", func(context.Context, []byte, TaskContext) (engine.MapOperator, error) { return &policyMap{}, nil })
	reg.RegisterSink("dlq", func(context.Context, []byte, TaskContext) (engine.SinkOperator, error) { return dlq, nil })
	err := newTaskExecutor(reg).run(context.Background(), "job", "task", rpc.TaskDescriptor{OperatorChain: []rpc.OperatorDescriptor{
		{OperatorID: "source", Type: rpc.OperatorTypeSource, ClassName: "source"},
		{OperatorID: "parse", Type: rpc.OperatorTypeMap, ClassName: "parse", ErrorPolicy: &rpc.ErrorPolicy{OnExhausted: "dlq"}, DLQSink: &rpc.DLQSinkDescriptor{ClassName: "dlq"}},
	}}, zerolog.Nop(), nil)
	if err == nil || dlq.opened || len(dlq.events) != 0 {
		t.Fatalf("err=%v sink=%+v", err, dlq)
	}
}

func TestTaskExecutorRejectsTransactionalRecordRetries(t *testing.T) {
	reg := NewRegistry()
	sink := &transactionalPolicySink{}
	reg.RegisterSource("source", func(context.Context, []byte, TaskContext) (engine.SourceOperator, error) { return &policySource{}, nil })
	reg.RegisterSink("txn", func(context.Context, []byte, TaskContext) (engine.SinkOperator, error) { return sink, nil })
	running := false
	err := newTaskExecutor(reg).run(context.Background(), "job", "task", rpc.TaskDescriptor{OperatorChain: []rpc.OperatorDescriptor{
		{OperatorID: "source", Type: rpc.OperatorTypeSource, ClassName: "source"},
		{OperatorID: "sink", Type: rpc.OperatorTypeSink, ClassName: "txn", ErrorPolicy: &rpc.ErrorPolicy{MaxRetries: 1}},
	}}, zerolog.Nop(), func() { running = true })
	if err == nil || running || sink.opened || len(sink.events) != 0 {
		t.Fatalf("err=%v running=%t sink=%+v", err, running, sink)
	}
}
