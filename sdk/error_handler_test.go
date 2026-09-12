package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestEmbeddedErrorPolicyDropsOnlyFailedRecord(t *testing.T) {
	env := New()
	sink := &collectSink{}
	stream := env.AddSource(&sliceSource{events: []Event{{Value: []byte("good")}, {Value: []byte("bad")}, {Value: []byte("good")}}}).MapWithName("parse", func(e Event) (Event, error) {
		if string(e.Value) == "bad" {
			return Event{}, errors.New("invalid record")
		}
		return e, nil
	}).WithErrorHandler(ErrorHandler{OnExhausted: "drop"})
	stream.AddSink(sink)
	result, err := env.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := len(sink.Events()); got != 2 {
		t.Fatalf("delivered %d records, want 2", got)
	}
	graph := env.graph.toJobGraph(1)
	for _, op := range graph.Operators {
		if op.Name == "parse" {
			if op.ErrorPolicy == nil || op.ErrorPolicy.OnExhausted != "drop" {
				t.Fatal("cluster conversion lost policy")
			}
			return
		}
	}
	t.Fatal("missing parse operator")
}

func TestEmbeddedErrorPolicyDeliversDLQ(t *testing.T) {
	env := New()
	sink, dlq := &collectSink{}, &collectSink{}
	env.AddSource(&sliceSource{events: []Event{{Value: []byte("bad")}}}).MapWithName("parse", func(e Event) (Event, error) {
		return Event{}, errors.New("invalid record")
	}).WithErrorHandler(ErrorHandler{OnExhausted: "dlq"}).WithDLQSink(dlq).AddSink(sink)
	if _, err := env.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.Events()) != 0 || len(dlq.Events()) != 1 {
		t.Fatal("record not routed exclusively to DLQ")
	}
	var envelope struct {
		Original struct {
			Value []byte `json:"value"`
		} `json:"original_event"`
		Operator string `json:"operator"`
	}
	if err := json.Unmarshal(dlq.Events()[0].Value, &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope.Original.Value) != "bad" || envelope.Operator != "parse" {
		t.Fatalf("invalid envelope %+v", envelope)
	}
	if err := env.graph.validateForCluster(); err == nil {
		t.Fatal("inline DLQ silently discarded in cluster mode")
	}
}

func TestEmbeddedErrorPolicyRetriesTransient(t *testing.T) {
	env := New()
	sink := &collectSink{}
	attempts := 0
	env.AddSource(&sliceSource{events: []Event{{Value: []byte("value")}}}).Map(func(e Event) (Event, error) {
		attempts++
		if attempts < 3 {
			return Event{}, ErrTransient
		}
		return e, nil
	}).WithErrorHandler(ErrorHandler{MaxRetries: 2, Backoff: "fixed", InitialDelayMS: 1}).AddSink(sink)
	if _, err := env.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || len(sink.Events()) != 1 {
		t.Fatalf("attempts=%d delivered=%d", attempts, len(sink.Events()))
	}
}
