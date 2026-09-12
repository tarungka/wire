package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if !dlq.opened || !dlq.closed || len(dlq.events) != 1 {
		t.Fatalf("DLQ lifecycle/output: %+v", dlq)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(dlq.events[0].Value, &record); err != nil {
		t.Fatal(err)
	}
	if string(record["operator"]) != "\"parse\"" {
		t.Fatalf("wrong operator: %s", record["operator"])
	}
}
