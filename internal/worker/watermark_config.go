package worker

import (
	"fmt"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func taskWatermarkConfig(chain []rpc.OperatorDescriptor) (*engine.WatermarkConfig, error) {
	var result *engine.WatermarkConfig
	for _, operator := range chain {
		cfg := operator.Watermark
		if cfg == nil {
			continue
		}
		if operator.Type != rpc.OperatorTypeSource {
			return nil, fmt.Errorf("watermark generation configuration requires a source: %s", operator.OperatorID)
		}
		if result != nil {
			return nil, fmt.Errorf("multiple watermark sources in one task")
		}
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		resolved := engine.WatermarkConfig{EmitInterval: cfg.EmitInterval, IdleTimeout: cfg.IdleTimeout}
		switch cfg.Strategy {
		case "bounded-ooo":
			resolved.Strategy = engine.StrategyBoundedOOO
			if cfg.MaxOOO != nil {
				resolved.MaxOOO = *cfg.MaxOOO
				if *cfg.MaxOOO == 0 {
					resolved.Strategy = engine.StrategyMonotonic
				}
			}
		case "monotonic":
			resolved.Strategy = engine.StrategyMonotonic
		case "ingestion-time":
			resolved.Strategy = engine.StrategyIngestionTime
		}
		result = &resolved
	}
	return result, nil
}
