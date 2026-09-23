package sdk

import (
	"bytes"
	"encoding/json"
)

// reduceWindowAggregator keeps one reduced record per window. Checked callbacks
// propagate errors before the processor installs a new accumulator.
type reduceWindowAggregator struct{ fn ReduceFunc }

func (reduceWindowAggregator) CreateAccumulator() []byte   { return nil }
func (reduceWindowAggregator) GetResult(acc []byte) []byte { return acc }
func (a reduceWindowAggregator) Add(acc []byte, event Event) []byte {
	b, err := a.AddChecked(acc, event)
	if err != nil {
		panic(err)
	}
	return b
}
func (a reduceWindowAggregator) Merge(x, y []byte) []byte {
	b, err := a.MergeChecked(x, y)
	if err != nil {
		panic(err)
	}
	return b
}
func (a reduceWindowAggregator) AddChecked(acc []byte, event Event) ([]byte, error) {
	if len(acc) == 0 {
		return json.Marshal(event)
	}
	var previous Event
	if err := json.Unmarshal(acc, &previous); err != nil {
		return nil, err
	}
	next, err := a.fn(previous, event)
	if err != nil {
		return nil, err
	}
	return json.Marshal(next)
}
func (a reduceWindowAggregator) MergeChecked(x, y []byte) ([]byte, error) {
	if len(y) == 0 {
		return bytes.Clone(x), nil
	}
	var event Event
	if err := json.Unmarshal(y, &event); err != nil {
		return nil, err
	}
	return a.AddChecked(x, event)
}
func (reduceWindowAggregator) ResultChecked(acc []byte) ([]byte, error) { return bytes.Clone(acc), nil }

// applyWindowAggregator retains input records, bounded by the window state's
// payload limit. Length-prefixed JSON isn't necessary: concatenated JSON values
// can be decoded in order, and append avoids decoding all earlier records.
type applyWindowAggregator struct{}

func (applyWindowAggregator) CreateAccumulator() []byte { return nil }
func (applyWindowAggregator) Add(acc []byte, event Event) []byte {
	data, err := json.Marshal(event)
	if err != nil {
		panic(err)
	}
	return append(acc, data...)
}
func (applyWindowAggregator) GetResult(acc []byte) []byte { return acc }
func (applyWindowAggregator) Merge(x, y []byte) []byte    { return append(x, y...) }
