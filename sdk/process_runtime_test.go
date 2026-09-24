package sdk

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestProcessTimerStateAndRestore(t *testing.T) {
	tag := NewOutputTag("audit")
	fn := func(c ProcessContext, e Event) ([]Event, error) {
		if c.CurrentEventTime() != e.EventTime || string(c.CurrentKey()) != string(e.Key) {
			return nil, fmt.Errorf("wrong Process context")
		}
		c.GetState("value").Set(e.Value)
		c.GetListState("list").Add(e.Value)
		c.GetMapState("map").Put("value", e.Value)
		c.RegisterEventTimeTimer(e.EventTime + 10)
		c.RegisterEventTimeTimer(e.EventTime + 10) // Same key/deadline is deduplicated.
		c.EmitToSideOutput(tag, e)
		return nil, nil
	}
	onTimer := func(c ProcessContext, ts int64) ([]Event, error) {
		if c.CurrentWatermark() < ts || c.CurrentEventTime() != ts {
			return nil, fmt.Errorf("wrong timer context")
		}
		if len(c.GetListState("list").Get()) != 1 || !reflect.DeepEqual(c.GetMapState("map").Get("value"), c.GetState("value").Get()) {
			return nil, fmt.Errorf("lost state")
		}
		return []Event{{Key: c.Key(), Value: c.GetState("value").Get(), EventTime: ts}}, nil
	}
	original, err := NewProcessHarness(fn, onTimer, tag)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = original.Close() }()
	for _, e := range []Event{{Key: []byte("a"), Value: []byte("first"), EventTime: 1}, {Key: []byte("b"), Value: []byte("second"), EventTime: 2}} {
		if _, err := original.Process(e); err != nil {
			t.Fatal(err)
		}
	}
	if len(original.SideOutput(tag)) != 2 {
		t.Fatal("side output lost")
	}
	if out, err := original.AdvanceWatermark(10); err != nil || len(out) != 0 {
		t.Fatalf("early timer: %v %v", out, err)
	}
	snapshot, err := original.Snapshot(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := original.AdvanceWatermark(100); err != nil {
		t.Fatal(err)
	}
	restored, err := NewProcessHarness(fn, onTimer, tag)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	if err := restored.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	out, err := restored.AdvanceWatermark(12)
	if err != nil || len(out) != 2 || string(out[0].Key) != "a" || string(out[1].Key) != "b" || string(out[0].Value) != "first" {
		t.Fatalf("restored timers=%v %v", out, err)
	}
	if out, err := restored.AdvanceWatermark(100); err != nil || len(out) != 0 {
		t.Fatalf("duplicate timers=%v %v", out, err)
	}
}

func TestProcessSideOutputRoutesAndBoundedTimerFires(t *testing.T) {
	env := New()
	main, side := &collectSink{}, &collectSink{}
	tag := NewOutputTag("audit")
	stream := env.AddSource(&sliceSource{events: []Event{{Key: []byte("k"), Value: []byte("input"), EventTime: 5}}}).KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).ProcessWithTimers(func(c ProcessContext, e Event) ([]Event, error) {
		c.EmitToSideOutput(tag, e)
		c.RegisterEventTimeTimer(10)
		return nil, nil
	}, func(c ProcessContext, ts int64) ([]Event, error) {
		return []Event{{Key: c.Key(), Value: []byte("timer"), EventTime: ts}}, nil
	}).WithSideOutputs(tag)
	stream.AddSink(main)
	stream.GetSideOutput(tag).AddSink(side)
	if _, err := env.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(main.Events()) != 1 || string(main.Events()[0].Value) != "timer" || len(side.Events()) != 1 || string(side.Events()[0].Value) != "input" {
		t.Fatalf("main=%v side=%v", main.Events(), side.Events())
	}
}

func TestTimerCallbacksCanCancelOtherDueTimers(t *testing.T) {
	op := &processAdapter{config: NewHashMapStateBackend(0)}
	op.onTimer = func(c ProcessContext, ts int64) ([]Event, error) {
		c.DeleteEventTimeTimer(ts + 1)
		return []Event{{EventTime: ts}}, nil
	}
	if err := op.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = op.Close() }()
	for _, ts := range []int64{1, 2} {
		if err := op.backend.Put(timerKey([]byte("key"), ts), []byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := op.OnWatermark(t.Context(), 3)
	if err != nil || len(out) != 1 || out[0].EventTime != 1 {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestProcessSideOutputErrorDoesNotEmit(t *testing.T) {
	h, err := NewProcessHarness(func(c ProcessContext, e Event) ([]Event, error) {
		c.EmitToSideOutput(NewOutputTag("unknown"), e)
		return []Event{e}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	if out, err := h.Process(engine.Event{Value: []byte("value")}); err == nil || len(out) != 0 {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestLateRecordTimerFiresWithoutAnotherWatermark(t *testing.T) {
	h, err := NewProcessHarness(func(c ProcessContext, e Event) ([]Event, error) {
		c.RegisterEventTimeTimer(e.EventTime + 1)
		return nil, nil
	}, func(c ProcessContext, ts int64) ([]Event, error) { return []Event{{EventTime: ts, Key: c.Key()}}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	if _, err := h.AdvanceWatermark(100); err != nil {
		t.Fatal(err)
	}
	out, err := h.Process(Event{Key: []byte("late"), EventTime: 10})
	if err != nil || len(out) != 1 || out[0].EventTime != 11 {
		t.Fatalf("late timer=%v err=%v", out, err)
	}
}

func TestManagedProcessRetriesRollbackState(t *testing.T) {
	env := New().SetStateBackend(NewHashMapStateBackend(0))
	attempts := 0
	stream := env.AddSource(&sliceSource{events: []Event{{Key: []byte("k"), Value: []byte("input")}}}).KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).Process(func(c ProcessContext, e Event) ([]Event, error) {
		state := c.GetState("count")
		value, err := state.ValueInt64()
		if err != nil {
			return nil, err
		}
		if err := state.SetInt64(value + 1); err != nil {
			return nil, err
		}
		c.GetMapState("map").Put("x", []byte("value"))
		if len(c.GetMapState("map").Keys()) != 1 {
			return nil, fmt.Errorf("pending map state is invisible")
		}
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("%w: retry this record", ErrTransient)
		}
		value, err = state.ValueInt64()
		if err != nil {
			return nil, err
		}
		return []Event{{Value: []byte(fmt.Sprint(value))}}, nil
	})
	env.graph.nodes[stream.nodeID].ErrorPolicy = &rpc.ErrorPolicy{MaxRetries: 1}
	sink := &collectSink{}
	stream.AddSink(sink)
	if _, err := env.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(sink.Events()) != 1 || string(sink.Events()[0].Value) != "1" {
		t.Fatalf("attempts=%d events=%v", attempts, sink.Events())
	}
}

func TestFailedProcessDiscardsStateTimersAndSideOutput(t *testing.T) {
	tag := NewOutputTag("audit")
	h, err := NewProcessHarness(func(c ProcessContext, e Event) ([]Event, error) {
		state := c.GetState("counter")
		n, err := state.ValueInt64()
		if err != nil {
			return nil, err
		}
		if err := state.SetInt64(n + 1); err != nil {
			return nil, err
		}
		if string(e.Value) == "bad" {
			c.RegisterEventTimeTimer(10)
			c.EmitToSideOutput(tag, e)
			return nil, fmt.Errorf("failed invocation")
		}
		return []Event{{Value: []byte(fmt.Sprint(n + 1))}}, nil
	}, func(ProcessContext, int64) ([]Event, error) { return []Event{{Value: []byte("leaked timer")}}, nil }, tag)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	if _, err := h.Process(Event{Key: []byte("k"), Value: []byte("bad")}); err == nil {
		t.Fatal("failure lost")
	}
	out, err := h.Process(Event{Key: []byte("k"), Value: []byte("good")})
	if err != nil || len(out) != 1 || string(out[0].Value) != "1" || len(h.SideOutput(tag)) != 0 {
		t.Fatalf("partial invocation leaked: %v %v", out, err)
	}
	if timers, err := h.AdvanceWatermark(100); err != nil || len(timers) != 0 {
		t.Fatalf("failed invocation registered timer: %v %v", timers, err)
	}
}

func TestTimerCallbacksPreserveDeadlineOrderForNewTimers(t *testing.T) {
	h, err := NewProcessHarness(func(c ProcessContext, _ Event) ([]Event, error) {
		c.RegisterEventTimeTimer(10)
		c.RegisterEventTimeTimer(20)
		return nil, nil
	}, func(c ProcessContext, ts int64) ([]Event, error) {
		if ts == 10 {
			c.RegisterEventTimeTimer(15)
		}
		return []Event{{EventTime: ts}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	if _, err := h.Process(Event{Key: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	out, err := h.AdvanceWatermark(100)
	if err != nil || len(out) != 3 || out[0].EventTime != 10 || out[1].EventTime != 15 || out[2].EventTime != 20 {
		t.Fatalf("timer ordering=%v err=%v", out, err)
	}
}
