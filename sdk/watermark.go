package sdk

import (
	"time"

	"github.com/tarungka/wire/internal/engine"

	"github.com/tarungka/wire/internal/rpc"
)

// WatermarkStrategy selects source timestamp and periodic watermark behavior.
// Configure one of the built-in strategies; custom generators are not supported.
type WatermarkStrategy struct{ config rpc.WatermarkConfig }

func BoundedOutOfOrderness(maxOOO time.Duration) WatermarkStrategy {
	return WatermarkStrategy{config: rpc.WatermarkConfig{Strategy: "bounded-ooo", MaxOOO: &maxOOO}}
}
func MonotonicTimestamps() WatermarkStrategy {
	return WatermarkStrategy{config: rpc.WatermarkConfig{Strategy: "monotonic"}}
}
func IngestionTime() WatermarkStrategy {
	return WatermarkStrategy{config: rpc.WatermarkConfig{Strategy: "ingestion-time"}}
}

// WithEmitInterval returns a copy with the periodic emission interval.
// Zero uses 200ms. Negative durations fail graph validation.
func (s WatermarkStrategy) WithEmitInterval(interval time.Duration) WatermarkStrategy {
	s.config.EmitInterval = interval
	return s
}

// WithIdleTimeout returns a copy with the input idle timeout. Zero uses one minute.
func (s WatermarkStrategy) WithIdleTimeout(timeout time.Duration) WatermarkStrategy {
	s.config.IdleTimeout = timeout
	return s
}

// SetWatermarkStrategy configures a source. Validation at Execute rejects
// placement on a non-source and invalid strategy parameters.
func (ds *DataStream) SetWatermarkStrategy(strategy WatermarkStrategy) *DataStream {
	cfg := strategy.config
	ds.env.graph.nodes[ds.nodeID].Watermark = &cfg
	return ds
}

func embeddedWatermark(nodes []*StreamNode) (engine.WatermarkStrategy, time.Duration) {
	for _, node := range nodes {
		if node.Type != NodeSource {
			continue
		}
		if node.Watermark == nil {
			return engine.NewBoundedOutOfOrdernessStrategy(engine.DefaultMaxOOO), engine.DefaultWatermarkInterval
		}
		cfg := node.Watermark
		switch cfg.Strategy {
		case "bounded-ooo":
			if cfg.MaxOOO == nil {
				return engine.NewBoundedOutOfOrdernessStrategy(engine.DefaultMaxOOO), cfg.EmitInterval
			}
			if *cfg.MaxOOO == 0 {
				return engine.NewMonotonicTimestampsStrategy(), cfg.EmitInterval
			}
			return engine.NewBoundedOutOfOrdernessStrategy(*cfg.MaxOOO), cfg.EmitInterval
		case "monotonic":
			return engine.NewMonotonicTimestampsStrategy(), cfg.EmitInterval
		case "ingestion-time":
			return engine.NewIngestionTimeStrategy(), cfg.EmitInterval
		}
	}
	return nil, 0
}
