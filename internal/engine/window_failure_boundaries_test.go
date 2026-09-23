package engine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

type checkedWindowFailure struct {
	windowCount
	fail string
}

var errWindowFunction = errors.New("window function failed")

func (a *checkedWindowFailure) AddChecked(acc []byte, e Event) ([]byte, error) {
	if a.fail == "add" {
		acc[0] = 99
		return nil, errWindowFunction
	}
	return a.Add(acc, e), nil
}
func (a *checkedWindowFailure) MergeChecked(x, y []byte) ([]byte, error) {
	if a.fail == "merge" {
		x[0] = 99
		y[0] = 99
		return nil, errWindowFunction
	}
	return a.Merge(x, y), nil
}
func (a *checkedWindowFailure) ResultChecked(acc []byte) ([]byte, error) {
	if a.fail == "result" {
		acc[0] = 99
		return nil, errWindowFunction
	}
	return a.GetResult(acc), nil
}

func TestWindowCheckedFailuresPreserveSnapshot(t *testing.T) {
	for _, kind := range []string{"tumbling", "sliding", "session"} {
		for _, failure := range []string{"add", "merge", "result", "watermark"} {
			if failure == "merge" && kind != "session" {
				continue
			}
			t.Run(kind+"/"+failure, func(t *testing.T) {
				a := &checkedWindowFailure{}
				p, err := NewWindowProcessor(WindowConfig{Kind: kind, Size: 10, Slide: 5, Gap: 10, AllowedLateness: 30, AggregationID: "checked-v1"}, a)
				if err != nil {
					t.Fatal(err)
				}
				addWindowEvent(t, p, 1)
				if failure != "watermark" {
					p.AdvanceWatermark(11)
				}
				before, err := p.Checkpoint(1)
				if err != nil {
					t.Fatal(err)
				}
				a.fail = failure
				if failure == "watermark" {
					a.fail = "result"
					_, err = p.AdvanceWatermarkChecked(11)
				} else {
					_, _, err = p.Process(Event{Key: []byte("k"), EventTime: 1})
				}
				if !errors.Is(err, errWindowFunction) {
					t.Fatalf("lost error: %v", err)
				}
				after, err := p.Checkpoint(1)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("failed callback changed checkpoint")
				}
				if failure == "watermark" {
					func() {
						defer func() {
							if recover() == nil {
								t.Fatal("legacy API hid failure")
							}
						}()
						p.AdvanceWatermark(11)
					}()
				}
				a.fail = ""
				if failure == "watermark" {
					if len(p.AdvanceWatermark(11)) == 0 {
						t.Fatal("lost initial result")
					}
				} else {
					results, late := addWindowEvent(t, p, 1)
					if late || len(results) == 0 {
						t.Fatal("retry lost update")
					}
					for _, r := range results {
						if !r.IsUpdate || binary.BigEndian.Uint64(r.Value) != 2 {
							t.Fatalf("corrupt retry: %+v", r)
						}
					}
				}
			})
		}
	}
}

func TestWindowTimestampExtremesAndLimits(t *testing.T) {
	for _, cfg := range []WindowConfig{{Kind: "tumbling", Size: 10, MaxStateBytes: -1}, {Kind: "tumbling", Size: 10, MaxWindows: -1}} {
		cfg.AggregationID = "count"
		if _, err := NewWindowProcessor(cfg, windowCount{}); err == nil {
			t.Fatal("accepted negative state limit")
		}
	}
	for _, kind := range []string{"tumbling", "sliding", "session"} {
		p := newTestWindow(t, WindowConfig{Kind: kind, Size: 10, Slide: 5, Gap: 10, AllowedLateness: 30})
		if _, _, err := p.Process(Event{EventTime: math.MaxInt64}); err == nil {
			t.Fatalf("%s accepted overflowing end", kind)
		}
		if kind != "session" {
			if _, _, err := p.Process(Event{EventTime: math.MinInt64}); err == nil {
				t.Fatalf("%s accepted underflowing start", kind)
			}
		}
	}
	p := newTestWindow(t, WindowConfig{Kind: "session", Gap: 10, AllowedLateness: 30})
	addWindowEvent(t, p, math.MaxInt64-20)
	if len(p.AdvanceWatermark(math.MaxInt64-10)) != 1 {
		t.Fatal("near-limit window did not fire")
	}
	if p.Stats().RetainedWindows != 1 {
		t.Fatal("retention deadline wrapped")
	}
	p.AdvanceWatermark(math.MaxInt64)
	if p.Stats().RetainedWindows != 0 {
		t.Fatal("saturated deadline did not purge")
	}
}

func TestWindowHundredLateUpdates(t *testing.T) {
	for _, kind := range []string{"tumbling", "sliding", "session"} {
		t.Run(kind, func(t *testing.T) {
			p := newTestWindow(t, WindowConfig{Kind: kind, Size: 10, Slide: 5, Gap: 10, AllowedLateness: 30})
			addWindowEvent(t, p, 1)
			initial := p.AdvanceWatermark(11)
			for i := uint64(2); i <= 101; i++ {
				results, late := addWindowEvent(t, p, 1)
				if late || len(results) != len(initial) {
					t.Fatal("late update missing")
				}
				for _, r := range results {
					if !r.IsUpdate || binary.BigEndian.Uint64(r.Value) != i {
						t.Fatalf("update %d: %+v", i, r)
					}
				}
			}
			if p.Stats().RetainedWindows != len(initial) {
				t.Fatal("updates grew window count")
			}
			p.AdvanceWatermark(41)
			if p.Stats().StateBytes != 0 {
				t.Fatal("updates leaked retained state")
			}
		})
	}
}
