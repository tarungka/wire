package engine

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"sync"
)

// WindowAggregator is structurally compatible with SDK Aggregator. Callers
// must use the same versioned AggregationID when restoring its accumulator.
// Merge must be associative and deterministic for session aggregation. Callbacks
// run under the processor lock and must not call back into the processor.
type WindowAggregator interface {
	CreateAccumulator() []byte
	Add([]byte, Event) []byte
	GetResult([]byte) []byte
	Merge([]byte, []byte) []byte
}

type WindowConfig struct {
	Kind             string // tumbling, sliding, or session
	Size, Slide, Gap int64  // milliseconds
	AllowedLateness  int64  // milliseconds after window end
	AggregationID    string
	MaxWindows       int
}

type WindowResult struct {
	Key                    []byte
	WindowStart, WindowEnd int64
	Value                  []byte
	IsUpdate               bool
}

type WindowStats struct {
	Late, Allowed, Dropped uint64
	RetainedWindows        int
}

type retainedWindow struct {
	Key         []byte
	Start, End  int64
	Accumulator []byte
	Fired       bool
	Emitted     bool
}

// WindowProcessor implements event-time window/lateness semantics. The caller
// must deliver ordered events/watermarks and route Process's too-late result to
// its configured side output. It does not run a watermark or checkpoint loop.
type WindowProcessor struct {
	mu         sync.Mutex
	config     WindowConfig
	aggregator WindowAggregator
	watermark  int64
	windows    map[string][]retainedWindow
	stats      WindowStats
}

func NewWindowProcessor(c WindowConfig, aggregator WindowAggregator) (*WindowProcessor, error) {
	if c.AllowedLateness < 0 || c.AggregationID == "" || aggregator == nil {
		return nil, fmt.Errorf("window: invalid lateness or aggregator")
	}
	if c.MaxWindows == 0 {
		c.MaxWindows = 100000
	}
	if c.MaxWindows < 1 {
		return nil, fmt.Errorf("window: invalid state limit")
	}
	switch c.Kind {
	case "tumbling":
		if c.Size <= 0 {
			return nil, fmt.Errorf("window: positive size required")
		}
	case "sliding":
		if c.Size <= 0 || c.Slide <= 0 {
			return nil, fmt.Errorf("window: positive size and slide required")
		}
		if (c.Size-1)/c.Slide+1 > int64(c.MaxWindows) {
			return nil, fmt.Errorf("window: assignment exceeds state limit")
		}
	case "session":
		if c.Gap <= 0 {
			return nil, fmt.Errorf("window: positive session gap required")
		}
	default:
		return nil, fmt.Errorf("window: unknown kind %q", c.Kind)
	}
	return &WindowProcessor{config: c, aggregator: aggregator, watermark: math.MinInt64, windows: make(map[string][]retainedWindow)}, nil
}
func windowEnd(start, duration int64) (int64, error) {
	if start > math.MaxInt64-duration {
		return 0, fmt.Errorf("window: timestamp overflows window end")
	}
	return start + duration, nil
}
func (p *WindowProcessor) deadline(end int64) int64 {
	if end > math.MaxInt64-p.config.AllowedLateness {
		return math.MaxInt64
	}
	return end + p.config.AllowedLateness
}
func floorWindowStart(timestamp, step int64) (int64, error) {
	remainder := timestamp % step
	if remainder < 0 {
		remainder += step
	}
	if timestamp < math.MinInt64+remainder {
		return 0, fmt.Errorf("window: timestamp underflows window start")
	}
	return timestamp - remainder, nil
}
func cloneWindow(w retainedWindow) retainedWindow {
	w.Key = bytes.Clone(w.Key)
	w.Accumulator = bytes.Clone(w.Accumulator)
	return w
}
func (p *WindowProcessor) result(w retainedWindow, update bool) WindowResult {
	return WindowResult{Key: bytes.Clone(w.Key), WindowStart: w.Start, WindowEnd: w.End, Value: bytes.Clone(p.aggregator.GetResult(bytes.Clone(w.Accumulator))), IsUpdate: update}
}

// Process returns updated results and whether all assigned windows were too
// late. A true tooLate result means the original event should be dropped or
// sent once to the caller's late-output handler. Rejected state growth is atomic.
func (p *WindowProcessor) Process(event Event) (results []WindowResult, tooLate bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := string(event.Key)
	existing := p.windows[key]
	next := make([]retainedWindow, len(existing))
	for i, w := range existing {
		next[i] = cloneWindow(w)
	}
	var assigned []retainedWindow
	if p.config.Kind == "session" {
		end, err := windowEnd(event.EventTime, p.config.Gap)
		if err != nil {
			return nil, false, err
		}
		merged := retainedWindow{Key: bytes.Clone(event.Key), Start: event.EventTime, End: end, Accumulator: p.aggregator.CreateAccumulator()}
		changed := true
		for changed {
			changed = false
			remaining := next[:0]
			for _, w := range next {
				if w.Start <= merged.End && w.End >= merged.Start {
					merged.Start = min(merged.Start, w.Start)
					merged.End = max(merged.End, w.End)
					merged.Accumulator = p.aggregator.Merge(merged.Accumulator, bytes.Clone(w.Accumulator))
					merged.Emitted = merged.Emitted || w.Emitted
					changed = true
				} else {
					remaining = append(remaining, w)
				}
			}
			next = remaining
		}
		assigned = []retainedWindow{merged}
	} else {
		step := p.config.Size
		if p.config.Kind == "sliding" {
			step = p.config.Slide
		}
		start, err := floorWindowStart(event.EventTime, step)
		if err != nil {
			return nil, false, err
		}
		for {
			end, err := windowEnd(start, p.config.Size)
			if err != nil {
				return nil, false, err
			}
			if event.EventTime >= end {
				break
			}
			assigned = append(assigned, retainedWindow{Key: bytes.Clone(event.Key), Start: start, End: end})
			if p.config.Kind == "tumbling" || start < math.MinInt64+step {
				break
			}
			start -= step
		}
	}
	accepted := 0
	for _, window := range assigned {
		if p.watermark >= p.deadline(window.End) {
			continue
		}
		accepted++
		found := -1
		if p.config.Kind != "session" {
			for i, w := range next {
				if w.Start == window.Start && w.End == window.End {
					window = w
					found = i
					break
				}
			}
		}
		if window.Accumulator == nil {
			window.Accumulator = p.aggregator.CreateAccumulator()
		}
		window.Accumulator = p.aggregator.Add(bytes.Clone(window.Accumulator), event)
		if p.watermark >= window.End {
			results = append(results, p.result(window, window.Emitted))
			window.Fired = true
			window.Emitted = true
		}
		if found >= 0 {
			next[found] = window
		} else {
			next = append(next, window)
		}
	}
	// A rejected session must not remove the retained windows considered for merge.
	if accepted == 0 {
		next = existing
	}
	newCount := p.stats.RetainedWindows - len(existing) + len(next)
	if newCount > p.config.MaxWindows {
		return nil, false, ErrMemoryLimitExceeded
	}
	sort.Slice(next, func(i, j int) bool { return next[i].Start < next[j].Start })
	if len(next) > 0 {
		p.windows[key] = next
	}
	p.stats.RetainedWindows = newCount
	if event.EventTime < p.watermark {
		p.stats.Late++
		if accepted > 0 {
			p.stats.Allowed++
		}
	}
	tooLate = len(assigned) > 0 && accepted == 0
	if tooLate {
		p.stats.Dropped++
	}
	return results, tooLate, nil
}

// AdvanceWatermark fires eligible windows and purges at end+allowedLateness.
// A regressing watermark is ignored. Results are ordered by key then start.
func (p *WindowProcessor) AdvanceWatermark(watermark int64) []WindowResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	if watermark <= p.watermark {
		return nil
	}
	p.watermark = watermark
	keys := make([]string, 0, len(p.windows))
	for key := range p.windows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var results []WindowResult
	for _, key := range keys {
		remaining := make([]retainedWindow, 0, len(p.windows[key]))
		for _, w := range p.windows[key] {
			if !w.Fired && watermark >= w.End {
				results = append(results, p.result(w, w.Emitted))
				w.Fired = true
				w.Emitted = true
			}
			if watermark < p.deadline(w.End) {
				remaining = append(remaining, w)
			} else {
				p.stats.RetainedWindows--
			}
		}
		if len(remaining) == 0 {
			delete(p.windows, key)
		} else {
			p.windows[key] = remaining
		}
	}
	return results
}
func (p *WindowProcessor) Stats() WindowStats { p.mu.Lock(); defer p.mu.Unlock(); return p.stats }
