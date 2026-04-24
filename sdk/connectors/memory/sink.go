package memory

import (
	"context"
	"sync"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/worker"
)

// SinkConfig is the msgpack-encoded configuration carried in
// OperatorDescriptor.Config for a memory sink.
type SinkConfig struct {
	// SinkID is a test-chosen identifier used to retrieve collected events
	// from the package-level registry via Collected(sinkID).
	SinkID string `codec:"id"`
}

// SinkFactory returns a worker.SinkFactory that builds a memory sink from
// SinkConfig bytes.
func SinkFactory() worker.SinkFactory {
	return func(_ context.Context, cfg []byte, _ worker.TaskContext) (engine.SinkOperator, error) {
		var sc SinkConfig
		if len(cfg) > 0 {
			if err := protocol.DecodeMsgPack(cfg, &sc); err != nil {
				return nil, err
			}
		}
		return &memorySink{id: sc.SinkID}, nil
	}
}

// memorySink appends each received event's Value to a slice stored in the
// package-level registry keyed by SinkID. Integration tests read back with
// Collected(sinkID).
type memorySink struct {
	id string
}

func (s *memorySink) Open(_ context.Context) error        { return nil }
func (s *memorySink) Close() error                        { return nil }
func (s *memorySink) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }

func (s *memorySink) Write(_ context.Context, event engine.Event) error {
	append_(s.id, event)
	return nil
}

var _ engine.SinkOperator = (*memorySink)(nil)

// sinkStore is the package-level registry of captured events, keyed by
// SinkID. It allows tests to retrieve output that was produced inside the
// worker's factory-instantiated sink.
var (
	sinkStoreMu sync.RWMutex
	sinkStore   = make(map[string][]engine.Event)
)

func append_(id string, e engine.Event) {
	sinkStoreMu.Lock()
	defer sinkStoreMu.Unlock()
	sinkStore[id] = append(sinkStore[id], e)
}

// Collected returns a copy of all events captured by the sink with the given
// SinkID. Intended for use only from tests.
func Collected(id string) []engine.Event {
	sinkStoreMu.RLock()
	defer sinkStoreMu.RUnlock()
	out := make([]engine.Event, len(sinkStore[id]))
	copy(out, sinkStore[id])
	return out
}

// Reset clears the captured events for a SinkID (or all SinkIDs if id=="").
// Intended for use only from tests.
func Reset(id string) {
	sinkStoreMu.Lock()
	defer sinkStoreMu.Unlock()
	if id == "" {
		sinkStore = make(map[string][]engine.Event)
		return
	}
	delete(sinkStore, id)
}
