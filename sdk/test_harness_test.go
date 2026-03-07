package sdk

import (
	"testing"
)

func TestHarnessRunMapIdentity(t *testing.T) {
	h := NewTestHarness()
	h.AddInput(
		Event{Value: []byte("a")},
		Event{Value: []byte("b")},
	)

	results, err := h.RunMap(func(e Event) (Event, error) {
		return e, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestHarnessRunFilterDropAll(t *testing.T) {
	h := NewTestHarness()
	h.AddInput(
		Event{Value: []byte("a")},
		Event{Value: []byte("b")},
	)

	results, err := h.RunFilter(func(e Event) (bool, error) {
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestHarnessRunFlatMapDoubling(t *testing.T) {
	h := NewTestHarness()
	h.AddInput(Event{Value: []byte("x")})

	results, err := h.RunFlatMap(func(e Event) ([]Event, error) {
		return []Event{e, e}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestHarnessRunProcessStateful(t *testing.T) {
	h := NewTestHarness()
	h.AddInput(
		Event{Key: []byte("k1"), Value: []byte("v1")},
		Event{Key: []byte("k1"), Value: []byte("v2")},
	)

	results, err := h.RunProcess(func(ctx ProcessContext, e Event) ([]Event, error) {
		state := ctx.GetValueState("counter")
		prev := state.Get()
		if prev == nil {
			state.Set([]byte{1})
		} else {
			state.Set([]byte{prev[0] + 1})
		}
		return []Event{e}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}
