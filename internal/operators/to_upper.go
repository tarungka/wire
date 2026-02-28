package operators

import (
	"bytes"
	"context"

	"github.com/tarungka/wire/internal/engine"
)

// ToUpperMap uppercases the event Value, preserving Key, EventTime, and Headers.
// Implements engine.MapOperator.
type ToUpperMap struct{}

func (t *ToUpperMap) Open(ctx context.Context) error       { return nil }
func (t *ToUpperMap) Close() error                         { return nil }
func (t *ToUpperMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }

func (t *ToUpperMap) Map(ctx context.Context, e engine.Event) (engine.Event, error) {
	return engine.Event{
		Key:       e.Key,
		Value:     bytes.ToUpper(e.Value),
		EventTime: e.EventTime,
		Headers:   e.Headers,
	}, nil
}
