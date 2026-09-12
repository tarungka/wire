package sdk

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/tarungka/wire/internal/engine"
)

// windowRuntime bridges SDK aggregators to the event-time processor. Its caller
// must deliver each watermark only after all preceding records are processed.
type windowRuntime struct {
	processor *engine.WindowProcessor
	reduce    *windowReduceAggregator
	apply     WindowFunc
	buffer    *windowEventBuffer
}

func newWindowRuntime(node *StreamNode) (*windowRuntime, error) {
	if node.Window == nil {
		return nil, fmt.Errorf("sdk: window assigner is required")
	}
	w := &windowRuntime{}
	var agg engine.WindowAggregator = node.Aggregator
	if node.Type == NodeReduce {
		if node.ReduceFn == nil {
			return nil, fmt.Errorf("sdk: reduce function is required")
		}
		w.reduce = &windowReduceAggregator{fn: node.ReduceFn}
		agg = w.reduce
	}
	if node.WindowFn != nil {
		w.apply = node.WindowFn
		w.buffer = &windowEventBuffer{}
		agg = w.buffer
	}
	if agg == nil {
		return nil, fmt.Errorf("sdk: window requires Aggregate or Reduce")
	}
	assigner := node.Window
	p, err := engine.NewWindowProcessor(engine.WindowConfig{Kind: assigner.Type(), Size: assigner.Size().Milliseconds(), Slide: assigner.Slide().Milliseconds(), Gap: assigner.Gap().Milliseconds(), AllowedLateness: node.AllowedLateness, AggregationID: "sdk-window-" + strconv.Itoa(node.ID)}, agg)
	if err != nil {
		return nil, err
	}
	w.processor = p
	return w, nil
}

func (w *windowRuntime) process(e Event) ([]Event, error) {
	results, _, err := w.processor.Process(e)
	if err == nil && w.reduce != nil {
		err = w.reduce.err
	}
	if w.buffer != nil && w.buffer.err != nil {
		err = w.buffer.err
	}
	if err != nil {
		return nil, err
	}
	return w.events(results)
}
func (w *windowRuntime) watermark(timestamp int64) ([]Event, error) {
	return w.events(w.processor.AdvanceWatermark(timestamp))
}
func (w *windowRuntime) events(results []engine.WindowResult) ([]Event, error) {
	if w.reduce != nil && w.reduce.err != nil {
		return nil, w.reduce.err
	}
	if w.buffer != nil && w.buffer.err != nil {
		return nil, w.buffer.err
	}
	events := make([]Event, 0, len(results))
	for _, r := range results {
		if w.apply != nil {
			var input []Event
			if err := json.Unmarshal(r.Value, &input); err != nil {
				return nil, err
			}
			output, err := w.apply(WindowInfo{Start: r.WindowStart, End: r.WindowEnd}, input)
			if err != nil {
				return nil, err
			}
			for _, event := range output {
				events = append(events, windowMetadata(event, r))
			}
			continue
		}
		e := Event{Key: r.Key, Value: r.Value, EventTime: r.WindowEnd}
		if w.reduce != nil {
			if err := json.Unmarshal(r.Value, &e); err != nil {
				return nil, err
			}
			e.Key = r.Key
			e.EventTime = r.WindowEnd
		}
		events = append(events, windowMetadata(e, r))
	}
	return events, nil
}

type windowReduceAggregator struct {
	fn  ReduceFunc
	err error
}

func (*windowReduceAggregator) CreateAccumulator() []byte     { return nil }
func (a *windowReduceAggregator) GetResult(acc []byte) []byte { return acc }
func (a *windowReduceAggregator) Add(acc []byte, e Event) []byte {
	if a.err != nil {
		return acc
	}
	if len(acc) > 0 {
		var previous Event
		if err := json.Unmarshal(acc, &previous); err != nil {
			a.err = err
			return acc
		}
		var err error
		e, err = a.fn(previous, e)
		if err != nil {
			a.err = err
			return acc
		}
	}
	data, err := json.Marshal(e)
	a.err = err
	return data
}
func (a *windowReduceAggregator) Merge(left, right []byte) []byte {
	if len(right) == 0 {
		return left
	}
	var e Event
	if err := json.Unmarshal(right, &e); err != nil {
		a.err = err
		return left
	}
	return a.Add(left, e)
}

func windowMetadata(e Event, r engine.WindowResult) Event {
	headers := make(map[string][]byte, len(e.Headers)+3)
	for k, v := range e.Headers {
		headers[k] = append([]byte(nil), v...)
	}
	headers["wire.window.start"] = []byte(strconv.FormatInt(r.WindowStart, 10))
	headers["wire.window.end"] = []byte(strconv.FormatInt(r.WindowEnd, 10))
	headers["wire.window.update"] = []byte(strconv.FormatBool(r.IsUpdate))
	e.Headers = headers
	e.Key = append([]byte(nil), r.Key...)
	e.EventTime = r.WindowEnd
	return e
}

type windowEventBuffer struct{ err error }

func (*windowEventBuffer) CreateAccumulator() []byte   { return []byte("[]") }
func (*windowEventBuffer) GetResult(acc []byte) []byte { return acc }
func (a *windowEventBuffer) Add(acc []byte, e Event) []byte {
	data, err := json.Marshal([]Event{e})
	if err != nil {
		a.err = err
		return acc
	}
	return a.Merge(acc, data)
}
func (a *windowEventBuffer) Merge(left, right []byte) []byte {
	if a.err != nil {
		return left
	}
	if len(left)+len(right) > 8<<20 {
		a.err = fmt.Errorf("sdk: Window.Apply retained window exceeds 8 MiB")
		return left
	}
	if len(left) <= 2 {
		return append([]byte(nil), right...)
	}
	if len(right) <= 2 {
		return left
	}
	result := append([]byte(nil), left[:len(left)-1]...)
	result = append(result, ',')
	return append(result, right[1:]...)
}
