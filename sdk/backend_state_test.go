package sdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

func TestEmbeddedBackendKeyedState(t *testing.T) {
	for _, kind := range []string{"hashmap", "pebble"} {
		t.Run(kind, func(t *testing.T) {
			cfg := NewHashMapStateBackend(1)
			if kind == "pebble" {
				cfg = NewPebbleStateBackend(t.TempDir())
			}
			run := func() map[string]int {
				env := New().SetParallelism(2).SetStateBackend(cfg)
				sink := &collectSink{}
				env.AddSource(&sliceSource{events: []Event{{Value: []byte("a")}, {Value: []byte("b")}, {Value: []byte("a")}}}).
					KeyBy(func(e Event) ([]byte, error) { return e.Value, nil }).
					Process(func(c ProcessContext, e Event) ([]Event, error) {
						value := c.GetValueState("same")
						n, _ := strconv.Atoi(string(value.Get()))
						n++
						value.Set([]byte(strconv.Itoa(n)))
						list := c.GetListState("same")
						list.Add([]byte("item"))
						m := c.GetMapState("same")
						m.Put("", []byte("empty"))
						m.Put("x", []byte("value"))
						if len(list.Get()) != n || string(m.Get("")) != "empty" || len(m.Keys()) != 2 {
							return nil, fmt.Errorf("state namespace collision or lost list")
						}
						e.Key = c.Key()
						e.Value = value.Get()
						return []Event{e}, nil
					}).AddSink(sink)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if _, err := env.Execute(ctx); err != nil {
					t.Fatal(err)
				}
				got := map[string]int{}
				for _, e := range sink.Events() {
					n, _ := strconv.Atoi(string(e.Value))
					if n > got[string(e.Key)] {
						got[string(e.Key)] = n
					}
				}
				return got
			}
			first := run()
			if first["a"] != 2 || first["b"] != 1 {
				t.Fatalf("keyed counts: %v", first)
			}
			second := run()
			wantA, wantB := 2, 1
			if kind == "pebble" {
				wantA, wantB = 4, 2
			}
			if second["a"] != wantA || second["b"] != wantB {
				t.Fatalf("second execution: %v", second)
			}
		})
	}
}
func TestBackendStateNamespacesAndClear(t *testing.T) {
	b := engine.NewHashMapStateBackend(0)
	defer func() { _ = b.Close() }()
	c1 := &backendProcessContext{key: []byte("a"), backend: b}
	c2 := &backendProcessContext{key: []byte("ab"), backend: b}
	c1.GetValueState("bc").Set([]byte("one"))
	c2.GetValueState("c").Set([]byte("two"))
	if string(c1.GetValueState("bc").Get()) != "one" {
		t.Fatal("ambiguous state key encoding")
	}
	value := c1.GetValueState("bc").Get()
	value[0] = 'X'
	if string(c1.GetValueState("bc").Get()) != "one" {
		t.Fatal("borrowed state bytes")
	}
	m := c1.GetMapState("m")
	m.Put("x", []byte("x"))
	m.Put("xy", []byte("y"))
	m.Clear()
	if len(m.Keys()) != 0 {
		t.Fatal("map clear failed")
	}
	c1.GetListState("l").Add([]byte("a"))
	c1.GetListState("l").Clear()
	if len(c1.GetListState("l").Get()) != 0 {
		t.Fatal("list clear failed")
	}
	c1.GetValueState("bc").Clear()
	if c1.GetValueState("bc").Get() != nil {
		t.Fatal("value clear failed")
	}
}
func TestEmbeddedBackendMemoryFailureCancelsRouter(t *testing.T) {
	env := New().SetStateBackend(NewHashMapStateBackend(1))
	sink := &collectSink{}
	records := make([]Event, 3*engine.DefaultInputBufferSize)
	env.AddSource(&sliceSource{events: records}).KeyBy(func(Event) ([]byte, error) { return []byte("key"), nil }).
		Process(func(c ProcessContext, e Event) ([]Event, error) {
			c.GetValueState("large").Set(bytes.Repeat([]byte("x"), 1<<20))
			return []Event{e}, nil
		}).AddSink(sink)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := env.Execute(ctx)
	if !errors.Is(err, engine.ErrMemoryLimitExceeded) {
		t.Fatalf("expected memory error, got %v", err)
	}
	if len(sink.Events()) != 0 {
		t.Fatal("emitted results after failed state update")
	}
}

func TestEmbeddedKeySelectorFailure(t *testing.T) {
	expected := errors.New("selector failed")
	env := New().SetStateBackend(NewHashMapStateBackend(1))
	env.AddSource(&sliceSource{events: make([]Event, 3*engine.DefaultInputBufferSize)}).
		KeyBy(func(Event) ([]byte, error) { return nil, expected }).
		Process(func(ProcessContext, Event) ([]Event, error) { return nil, nil }).AddSink(&collectSink{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := env.Execute(ctx); !errors.Is(err, expected) {
		t.Fatalf("selector error: %v", err)
	}
}

func TestStateBackendConfigValidation(t *testing.T) {
	for _, cfg := range []StateBackendConfig{{Type: "unknown"}, {Type: "hashmap", MaxMemoryMB: -1}} {
		if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config %+v: %v", cfg, err)
		}
	}

}
