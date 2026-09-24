package worker

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestTaskWatermarkConfiguration(t *testing.T) {
	for name, want := range map[string]engine.WatermarkStrategyType{"bounded-ooo": engine.StrategyBoundedOOO, "monotonic": engine.StrategyMonotonic, "ingestion-time": engine.StrategyIngestionTime} {
		cfg, err := taskWatermarkConfig([]rpc.OperatorDescriptor{{Type: rpc.OperatorTypeSource, Watermark: &rpc.WatermarkConfig{Strategy: name, EmitInterval: time.Second, IdleTimeout: 2 * time.Second}}})
		if err != nil || cfg.Strategy != want || cfg.EmitInterval != time.Second || cfg.IdleTimeout != 2*time.Second {
			t.Fatalf("%s: %+v %v", name, cfg, err)
		}
	}
	for _, operator := range []rpc.OperatorDescriptor{{Type: rpc.OperatorTypeMap, Watermark: &rpc.WatermarkConfig{Strategy: "monotonic"}}, {Type: rpc.OperatorTypeSource, Watermark: &rpc.WatermarkConfig{Strategy: "bad"}}} {
		if _, err := taskWatermarkConfig([]rpc.OperatorDescriptor{operator}); err == nil {
			t.Fatal("invalid watermark configuration accepted")
		}
	}
}

func TestZeroWatermarkToleranceIsExplicit(t *testing.T) {
	zero := time.Duration(0)
	cfg, err := taskWatermarkConfig([]rpc.OperatorDescriptor{{Type: rpc.OperatorTypeSource, Watermark: &rpc.WatermarkConfig{Strategy: "bounded-ooo", MaxOOO: &zero}}})
	if err != nil || cfg.Strategy != engine.StrategyMonotonic {
		t.Fatalf("explicit zero lost: %+v %v", cfg, err)
	}
}
