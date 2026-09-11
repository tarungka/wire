package httpapi

import (
	"context"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/worker"
)

func SourceFactory() worker.SourceFactory {
	return func(_ context.Context, data []byte, _ worker.TaskContext) (engine.SourceOperator, error) {
		var cfg SourceConfig
		if err := protocol.DecodeMsgPack(data, &cfg); err != nil {
			return nil, err
		}
		return NewSource(cfg)
	}
}
func SinkFactory() worker.SinkFactory {
	return func(_ context.Context, data []byte, _ worker.TaskContext) (engine.SinkOperator, error) {
		var cfg SinkConfig
		if err := protocol.DecodeMsgPack(data, &cfg); err != nil {
			return nil, err
		}
		return NewSink(cfg)
	}
}
