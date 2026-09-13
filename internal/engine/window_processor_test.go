package engine

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

type windowCount struct{}

func (windowCount) CreateAccumulator() []byte { return make([]byte, 8) }
func (windowCount) Add(acc []byte, _ Event) []byte {
	binary.BigEndian.PutUint64(acc, binary.BigEndian.Uint64(acc)+1)
	return acc
}
func (windowCount) GetResult(acc []byte) []byte { return acc }
func (windowCount) Merge(a, b []byte) []byte {
	binary.BigEndian.PutUint64(a, binary.BigEndian.Uint64(a)+binary.BigEndian.Uint64(b))
	return a
}
func newTestWindow(t *testing.T, c WindowConfig) *WindowProcessor {
	t.Helper()
	c.AggregationID = "count-v1"
	p, err := NewWindowProcessor(c, windowCount{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func addWindowEvent(t *testing.T, p *WindowProcessor, time int64) ([]WindowResult, bool) {
	t.Helper()
	results, late, err := p.Process(Event{Key: []byte("k"), EventTime: time})
	if err != nil {
		t.Fatal(err)
	}
	return results, late
}
func TestWindowAllowedLatenessAndPurgeBoundary(t *testing.T) {
	p := newTestWindow(t, WindowConfig{Kind: "tumbling", Size: 10, AllowedLateness: 5})
	if results, late := addWindowEvent(t, p, 2); len(results) != 0 || late {
		t.Fatal("on-time event rejected or fired early")
	}
	results := p.AdvanceWatermark(10)
	if len(results) != 1 || results[0].IsUpdate || binary.BigEndian.Uint64(results[0].Value) != 1 {
		t.Fatalf("initial fire: %+v", results)
	}
	results, late := addWindowEvent(t, p, 3)
	if late || len(results) != 1 || !results[0].IsUpdate || binary.BigEndian.Uint64(results[0].Value) != 2 {
		t.Fatalf("late update: %+v late=%t", results, late)
	}
	if p.AdvanceWatermark(9) != nil {
		t.Fatal("regressing watermark fired")
	}
	p.AdvanceWatermark(14)
	if p.Stats().RetainedWindows != 1 {
		t.Fatal("purged before deadline")
	}
	p.AdvanceWatermark(15)
	if p.Stats().RetainedWindows != 0 {
		t.Fatal("state retained at purge boundary")
	}
	if results, late = addWindowEvent(t, p, 4); len(results) != 0 || !late {
		t.Fatal("purged window resurrected")
	}
	stats := p.Stats()
	if stats.Late != 2 || stats.Allowed != 1 || stats.Dropped != 1 {
		t.Fatalf("stats %+v", stats)
	}
}
func TestWindowSlidingPartialLatenessAndNegativeTime(t *testing.T) {
	p := newTestWindow(t, WindowConfig{Kind: "sliding", Size: 10, Slide: 5})
	addWindowEvent(t, p, 7)
	p.AdvanceWatermark(10)
	// [0,10) is gone, but [5,15) is open. Route to late output only if
	// every assigned window is expired.
	if _, late := addWindowEvent(t, p, 8); late {
		t.Fatal("event eligible for one window dropped")
	}
	results := p.AdvanceWatermark(15)
	if len(results) != 1 || results[0].WindowStart != 5 || binary.BigEndian.Uint64(results[0].Value) != 2 {
		t.Fatalf("sliding output %+v", results)
	}
	negative := newTestWindow(t, WindowConfig{Kind: "tumbling", Size: 10})
	addWindowEvent(t, negative, -1)
	results = negative.AdvanceWatermark(0)
	if len(results) != 1 || results[0].WindowStart != -10 || results[0].WindowEnd != 0 {
		t.Fatalf("negative-time window %+v", results)
	}
}
func TestWindowSessionMergeAfterEmission(t *testing.T) {
	p := newTestWindow(t, WindowConfig{Kind: "session", Gap: 10, AllowedLateness: 30})
	addWindowEvent(t, p, 0)
	addWindowEvent(t, p, 20)
	first := p.AdvanceWatermark(15)
	if len(first) != 1 {
		t.Fatal("first session did not fire")
	}
	// Bridges [0,10] and [20,30] at the touching boundaries.
	if results, late := addWindowEvent(t, p, 10); late || len(results) != 0 {
		t.Fatalf("merged session should await extended end: %+v %t", results, late)
	}
	results := p.AdvanceWatermark(30)
	if len(results) != 1 || results[0].WindowStart != 0 || results[0].WindowEnd != 30 || !results[0].IsUpdate || binary.BigEndian.Uint64(results[0].Value) != 3 {
		t.Fatalf("merged session update: %+v", results)
	}
}
func TestWindowSnapshotRestoresProgressAndRejectsCorruption(t *testing.T) {
	cfg := WindowConfig{Kind: "tumbling", Size: 10, AllowedLateness: 5}
	p := newTestWindow(t, cfg)
	addWindowEvent(t, p, 1)
	p.AdvanceWatermark(10)
	snapshot, err := p.Checkpoint(7)
	if err != nil {
		t.Fatal(err)
	}
	restored := newTestWindow(t, cfg)
	if err = restored.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if results := restored.AdvanceWatermark(11); len(results) != 0 {
		t.Fatal("recovery refired unchanged window")
	}
	results, late := addWindowEvent(t, restored, 2)
	if late || len(results) != 1 || !results[0].IsUpdate || binary.BigEndian.Uint64(results[0].Value) != 2 {
		t.Fatal("recovery lost accumulator or update flag")
	}
	snapshot[0] ^= 1
	if err = restored.Restore(snapshot); !errors.Is(err, ErrSnapshotCorrupt) {
		t.Fatalf("corrupt snapshot: %v", err)
	}
	results, _ = addWindowEvent(t, restored, 3)
	if binary.BigEndian.Uint64(results[0].Value) != 3 {
		t.Fatal("failed restore changed live state")
	}
	restored.AdvanceWatermark(15)
	snapshot, err = restored.Checkpoint(8)
	if err != nil {
		t.Fatal(err)
	}
	afterPurge := newTestWindow(t, cfg)
	if err = afterPurge.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, late = addWindowEvent(t, afterPurge, 4); !late {
		t.Fatal("recovery resurrected expired window")
	}
}
func TestWindowStateLimitAndTimestampOverflow(t *testing.T) {
	p := newTestWindow(t, WindowConfig{Kind: "tumbling", Size: 10, MaxWindows: 1})
	addWindowEvent(t, p, 0)
	if _, _, err := p.Process(Event{EventTime: 20}); !errors.Is(err, ErrMemoryLimitExceeded) {
		t.Fatalf("state limit: %v", err)
	}
	if p.Stats().RetainedWindows != 1 {
		t.Fatal("failed allocation changed state")
	}
	if _, _, err := p.Process(Event{EventTime: math.MaxInt64}); err == nil {
		t.Fatal("overflowed window end")
	}
}

func TestWindowDefaultsAndConfigurationMismatch(t *testing.T) {
	for _, cfg := range []WindowConfig{
		{Kind: "unknown"}, {Kind: "tumbling"}, {Kind: "sliding", Size: 10, Slide: 0},
		{Kind: "session", Gap: -1}, {Kind: "tumbling", Size: 1, AllowedLateness: -1},
		{Kind: "sliding", Size: 100, Slide: 1, MaxWindows: 2},
	} {
		cfg.AggregationID = "count-v1"
		if _, err := NewWindowProcessor(cfg, windowCount{}); err == nil {
			t.Fatalf("accepted invalid config %+v", cfg)
		}
	}
	p := newTestWindow(t, WindowConfig{Kind: "tumbling", Size: 10})
	addWindowEvent(t, p, 1)
	p.AdvanceWatermark(10)
	if p.Stats().RetainedWindows != 0 {
		t.Fatal("zero lateness retained closed window")
	}
	if _, late := addWindowEvent(t, p, 1); !late {
		t.Fatal("zero lateness accepted closed-window event")
	}
	data, err := p.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	other := newTestWindow(t, WindowConfig{Kind: "tumbling", Size: 20})
	if err = other.Restore(data); !errors.Is(err, ErrSnapshotCorrupt) {
		t.Fatalf("configuration mismatch: %v", err)
	}
	if err = other.Restore(nil); !errors.Is(err, ErrSnapshotCorrupt) {
		t.Fatalf("empty snapshot: %v", err)
	}
}
