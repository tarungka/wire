// Package memory provides in-memory reference Source and Sink connectors
// for testing and examples. They are not intended for production use.
//
// Usage (in a user's wire-worker main):
//
//	worker.RegisterSource("memory-source", memory.SourceFactory())
//	worker.RegisterSink("memory-sink", memory.SinkFactory())
package memory

import (
	"context"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/worker"
)

// SourceConfig is the msgpack-encoded configuration carried in
// OperatorDescriptor.Config for a memory source.
type SourceConfig struct {
	// Events is the sequence of records the source will emit. Each
	// inner []byte is the Value; Key defaults to nil.
	Events [][]byte `codec:"e"`
}

// SourceFactory returns a worker.SourceFactory that builds a memory source
// from SourceConfig bytes.
func SourceFactory() worker.SourceFactory {
	return func(_ context.Context, cfg []byte, _ worker.TaskContext) (engine.SourceOperator, error) {
		var sc SourceConfig
		if len(cfg) > 0 {
			if err := protocol.DecodeMsgPack(cfg, &sc); err != nil {
				return nil, err
			}
		}
		return &memorySource{events: sc.Events}, nil
	}
}

// memorySource emits a fixed slice of events once and then signals EOF.
type memorySource struct {
	events [][]byte
	done   bool
}

func (s *memorySource) Open(_ context.Context) error        { return nil }
func (s *memorySource) Close() error                        { return nil }
func (s *memorySource) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }

func (s *memorySource) ReadBatch(_ context.Context) ([]engine.Event, error) {
	if s.done {
		return nil, nil
	}
	s.done = true
	out := make([]engine.Event, 0, len(s.events))
	for _, v := range s.events {
		out = append(out, engine.Event{Value: v})
	}
	return out, nil
}

// GenerateWatermark returns 0 — memory source emits untimed events.
func (s *memorySource) GenerateWatermark() int64 { return 0 }

var _ engine.SourceOperator = (*memorySource)(nil)
