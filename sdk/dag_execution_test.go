package sdk

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnionPreservesBothInputPipelines(t *testing.T) {
	env := New().SetParallelism(2)
	left := env.AddSource(&sliceSource{events: []Event{{Value: []byte("a")}}}).Map(func(e Event) (Event, error) { e.Value = append(e.Value, '1'); return e, nil })
	right := env.AddSource(&sliceSource{events: []Event{{Value: []byte("b")}}}).Map(func(e Event) (Event, error) { e.Value = append(e.Value, '2'); return e, nil })
	sink := &collectSink{}
	left.Union(right).AddSink(sink)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := env.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range sink.Events() {
		got = append(got, string(e.Value))
	}
	sort.Strings(got)
	if fmt.Sprint(got) != "[a1 b2]" {
		t.Fatalf("union outputs=%v", got)
	}
}

func TestBranchesPreserveIsolation(t *testing.T) {
	env := New()
	source := env.AddSource(&sliceSource{events: []Event{{Value: []byte("abc"), Headers: map[string][]byte{"h": []byte("original")}}}})
	a, b := &collectSink{}, &collectSink{}
	source.Map(func(e Event) (Event, error) { e.Value[0] = 'X'; e.Headers["h"][0] = 'X'; return e, nil }).AddSink(a)
	source.AddSink(b)
	if _, err := env.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(a.Events()) != 1 || string(a.Events()[0].Value) != "Xbc" || len(b.Events()) != 1 || string(b.Events()[0].Value) != "abc" || string(b.Events()[0].Headers["h"]) != "original" {
		t.Fatalf("branches: %v / %v", a.Events(), b.Events())
	}
}

func TestSourceTimestampAssignment(t *testing.T) {
	env := New()
	sink := &collectSink{}
	env.AddSource(&sliceSource{events: []Event{{Value: []byte("record")}}}).AssignTimestamps(func(Event) int64 { return 42 }).AddSink(sink)
	if _, err := env.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sink.Events()) != 1 || sink.Events()[0].EventTime != 42 {
		t.Fatalf("events=%v", sink.Events())
	}
}

type instanceSource struct {
	index          int
	read           bool
	opened, closed *atomic.Int32
}

func (s *instanceSource) Open(context.Context) error { s.opened.Add(1); return nil }
func (s *instanceSource) Close() error               { s.closed.Add(1); return nil }
func (s *instanceSource) GenerateWatermark() int64   { return 0 }
func (s *instanceSource) ReadBatch(context.Context) ([]Event, error) {
	if s.read {
		return nil, nil
	}
	s.read = true
	return []Event{{Value: []byte(fmt.Sprint(s.index))}}, nil
}
func TestParallelSourceFactoryLifecycle(t *testing.T) {
	env := New().SetParallelism(4)
	var opened, closed atomic.Int32
	sink := &collectSink{}
	env.AddSourceFactory("partitioned", func(c InstanceContext) (Source, error) {
		return &instanceSource{index: c.Index, opened: &opened, closed: &closed}, nil
	}).AddSink(sink)
	if _, err := env.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range sink.Events() {
		got = append(got, string(e.Value))
	}
	sort.Strings(got)
	if fmt.Sprint(got) != "[0 1 2 3]" || opened.Load() != 4 || closed.Load() != 4 {
		t.Fatalf("events=%v open=%d close=%d", got, opened.Load(), closed.Load())
	}
}

func TestConnectedInputsAndBroadcast(t *testing.T) {
	env := New().SetParallelism(2)
	first := env.AddSource(&sliceSource{events: []Event{{Value: []byte("a")}}})
	second := env.AddSource(&sliceSource{events: []Event{{Value: []byte("b")}}})
	sink := &collectSink{}
	first.Connect(second).CoMap(func(e Event) (Event, error) { e.Value = append(e.Value, '1'); return e, nil }, func(e Event) (Event, error) { e.Value = append(e.Value, '2'); return e, nil }).Broadcast().AddSink(sink)
	if _, err := env.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range sink.Events() {
		got = append(got, string(e.Value))
	}
	sort.Strings(got)
	if fmt.Sprint(got) != "[a1 a1 b2 b2]" {
		t.Fatalf("connected broadcast=%v", got)
	}
}

func TestEmbeddedRejectsMissingFunctionsBeforeOpeningSource(t *testing.T) {
	env := New()
	var opened, closed atomic.Int32
	env.AddSource(&instanceSource{opened: &opened, closed: &closed}).Map(nil).AddSink(&collectSink{})
	if _, err := env.Execute(t.Context()); err == nil {
		t.Fatal("nil map accepted")
	}
	if opened.Load() != 0 {
		t.Fatal("source opened before validation")
	}
}
