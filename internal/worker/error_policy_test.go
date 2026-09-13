package worker

import (
	"math"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCompileErrorPolicy(t *testing.T) {
	cfg, err := compileErrorPolicy(&rpc.ErrorPolicy{MaxRetries: 3, Backoff: "exponential", InitialDelayMS: 10, MaxDelayMS: 1000, Multiplier: 2, OnExhausted: "drop"}, "parse")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OnExhausted != engine.DropEvent || cfg.Backoff(1) != 20*time.Millisecond || cfg.Backoff(10000) != time.Second {
		t.Fatalf("incorrect retry policy: %+v", cfg)
	}
	for _, p := range []rpc.ErrorPolicy{
		{MaxRetries: -1}, {Backoff: "unknown"}, {OnExhausted: "unknown"},
		{InitialDelayMS: math.MaxInt64},
		{Backoff: "exponential", InitialDelayMS: 1, MaxDelayMS: 2, Multiplier: math.NaN()},
	} {
		if _, err := compileErrorPolicy(&p, "parse"); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
	cfg, err = compileErrorPolicy(nil, "sink")
	if err != nil || cfg.OnExhausted != engine.FailJob || cfg.MaxRetries != 0 {
		t.Fatal("default policy changed")
	}
}
