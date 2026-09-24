package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOutputBackpressureAccounting(t *testing.T) {
	samples := make(chan time.Duration, 2)
	ctx := context.WithValue(context.Background(), taskBackpressureKey{}, func(d time.Duration) { samples <- d })
	output := make(chan OutputMsg, 1)
	cc := &chainContext{ctx: ctx, outputCh: output}
	if err := cc.sendOutput(OutputMsg{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-samples:
		t.Fatal("ready send counted as blocked")
	default:
	}
	blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	cc.ctx = blocked
	if err := cc.sendOutput(OutputMsg{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked result: %v", err)
	}
	select {
	case duration := <-samples:
		if duration <= 0 {
			t.Fatalf("duration=%v", duration)
		}
	default:
		t.Fatal("cancelled blocked send not counted")
	}
	if len(output) != 1 {
		t.Fatal("cancelled send changed output")
	}
}
