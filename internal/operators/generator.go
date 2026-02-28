package operators

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

// GeneratorSource produces N synthetic events then signals EOF.
// Implements engine.SourceOperator.
type GeneratorSource struct {
	total   int
	counter int
	wm      atomic.Int64
}

// NewGeneratorSource creates a source that emits eventCount events.
func NewGeneratorSource(eventCount int) *GeneratorSource {
	return &GeneratorSource{total: eventCount}
}

func (g *GeneratorSource) Open(ctx context.Context) error       { return nil }
func (g *GeneratorSource) Close() error                         { return nil }
func (g *GeneratorSource) Checkpoint(id uint64) ([]byte, error) { return nil, nil }

func (g *GeneratorSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	if g.counter >= g.total {
		return nil, nil // EOF
	}
	now := time.Now().UnixMilli()
	g.wm.Store(now)
	e := engine.Event{
		Key:       []byte(fmt.Sprintf("%d", g.counter)),
		Value:     []byte(fmt.Sprintf("event-%d", g.counter)),
		EventTime: now,
	}
	g.counter++
	return []engine.Event{e}, nil
}

func (g *GeneratorSource) GenerateWatermark() int64 {
	return g.wm.Load()
}
