package operators

import (
	"context"
	"testing"

	"github.com/tarungka/wire/internal/engine"
)

func TestToUpperMap_UppercasesValue(t *testing.T) {
	ctx := context.Background()
	m := &ToUpperMap{}

	if err := m.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close()

	input := engine.Event{
		Key:       []byte("my-key"),
		Value:     []byte("hello world"),
		EventTime: 42,
		Headers:   map[string][]byte{"h1": []byte("v1")},
	}

	result, err := m.Map(ctx, input)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}

	if string(result.Value) != "HELLO WORLD" {
		t.Errorf("value: got %q, want %q", result.Value, "HELLO WORLD")
	}
	if string(result.Key) != "my-key" {
		t.Errorf("key: got %q, want %q", result.Key, "my-key")
	}
	if result.EventTime != 42 {
		t.Errorf("event time: got %d, want 42", result.EventTime)
	}
	if string(result.Headers["h1"]) != "v1" {
		t.Errorf("headers: got %q, want %q", result.Headers["h1"], "v1")
	}
}
