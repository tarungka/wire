package operators

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestGeneratorSource_EmitsNEvents(t *testing.T) {
	ctx := context.Background()
	n := 5
	g := NewGeneratorSource(n)

	if err := g.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer g.Close()

	for i := 0; i < n; i++ {
		batch, err := g.ReadBatch(ctx)
		if err != nil {
			t.Fatalf("ReadBatch[%d]: %v", i, err)
		}
		if len(batch) != 1 {
			t.Fatalf("ReadBatch[%d]: got %d events, want 1", i, len(batch))
		}
		e := batch[0]
		wantKey := fmt.Sprintf("%d", i)
		wantVal := fmt.Sprintf("event-%d", i)
		if string(e.Key) != wantKey {
			t.Errorf("event[%d] key: got %q, want %q", i, e.Key, wantKey)
		}
		if string(e.Value) != wantVal {
			t.Errorf("event[%d] value: got %q, want %q", i, e.Value, wantVal)
		}
		if e.EventTime <= 0 {
			t.Errorf("event[%d] event time should be positive, got %d", i, e.EventTime)
		}
	}

	// Next call should return nil (EOF).
	batch, err := g.ReadBatch(ctx)
	if err != nil {
		t.Fatalf("ReadBatch[EOF]: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil batch at EOF, got %d events", len(batch))
	}
}

func TestGeneratorSource_WatermarkConcurrency(t *testing.T) {
	ctx := context.Background()
	n := 100
	g := NewGeneratorSource(n)

	var wg sync.WaitGroup

	// ReadBatch from one goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			batch, err := g.ReadBatch(ctx)
			if err != nil {
				t.Errorf("ReadBatch error: %v", err)
				return
			}
			if batch == nil {
				return
			}
		}
	}()

	// GenerateWatermark from another goroutine concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n*2; i++ {
			_ = g.GenerateWatermark()
		}
	}()

	wg.Wait()
}

func TestGeneratorSource_Checkpoint(t *testing.T) {
	g := NewGeneratorSource(1)
	data, err := g.Checkpoint(1)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if data != nil {
		t.Fatalf("expected nil checkpoint data, got %v", data)
	}
}
