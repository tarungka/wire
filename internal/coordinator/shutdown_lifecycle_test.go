package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestSingleNodeShutdownStopsRun(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()
	c := New(CoordinatorConfig{NodeID: "node"}, store, nil, zerolog.Nop())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !c.IsReady() {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("coordinator not ready")
		}
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not stop Run independently of parent cancellation")
	}
	if ctx.Err() != nil {
		t.Fatal("test parent cancellation masked shutdown failure")
	}
}
