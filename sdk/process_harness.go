package sdk

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

// ProcessHarness exercises the production Process adapter, including keyed state,
// timers, side outputs and snapshots. Calls are serialized, as in an operator.
type ProcessHarness struct {
	operator *processAdapter
	side     map[string][]Event
}

func NewProcessHarness(fn ProcessFunc, onTimer TimerFunc, tags ...OutputTag) (*ProcessHarness, error) {
	op := &processAdapter{fn: fn, onTimer: onTimer, config: NewHashMapStateBackend(0)}
	for _, tag := range tags {
		op.sideTags = append(op.sideTags, tag.Name)
	}
	if err := op.Open(context.Background()); err != nil {
		return nil, err
	}
	return &ProcessHarness{operator: op, side: make(map[string][]Event)}, nil
}
func (h *ProcessHarness) collect(events []Event) []Event {
	var main []Event
	for _, event := range events {
		if tag := event.SideOutputTag(); tag != "" {
			h.side[tag] = append(h.side[tag], engine.WithSideOutput(event, ""))
		} else {
			main = append(main, event)
		}
	}
	return main
}
func (h *ProcessHarness) Process(event Event) ([]Event, error) {
	var events []Event
	err := h.operator.FlatMap(context.Background(), event, func(event Event) { events = append(events, event) })
	if err != nil {
		return nil, err
	}
	return h.collect(events), nil
}
func (h *ProcessHarness) AdvanceWatermark(timestamp int64) ([]Event, error) {
	events, err := h.operator.OnWatermark(context.Background(), timestamp)
	if err != nil {
		return nil, err
	}
	return h.collect(events), nil
}
func (h *ProcessHarness) SideOutput(tag OutputTag) []Event {
	var events []Event
	for _, event := range h.side[tag.Name] {
		events = append(events, cloneEvent(event))
	}
	return events
}
func (h *ProcessHarness) Snapshot(id uint64) ([]byte, error) { return h.operator.Checkpoint(id) }
func (h *ProcessHarness) Restore(snapshot []byte) error {
	var handle engine.SnapshotHandle
	if err := json.Unmarshal(snapshot, &handle); err != nil {
		return err
	}
	return h.operator.RestoreState(handle)
}
func (h *ProcessHarness) Close() error { return h.operator.Close() }

// SetProcessingTime supplies a deterministic TTL clock for tests. Event time
// and watermark advancement remain independent of this processing-time clock.
func (h *ProcessHarness) SetProcessingTime(now time.Time) {
	h.operator.clock = func() time.Time { return now }
}
