package sdk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"github.com/tarungka/wire/internal/engine"
)

// YAML uses JSON values at transform boundaries. Accumulators retain the SDK's
// binary format so checkpoint state does not change with result presentation.
type pipelineWindowAggregator struct {
	Aggregator
	kind string
}

func (a pipelineWindowAggregator) AddChecked(acc []byte, event Event) ([]byte, error) {
	if len(acc) != 8 {
		return nil, fmt.Errorf("invalid %s accumulator", a.kind)
	}
	if a.kind == "count" {
		if binary.BigEndian.Uint64(acc) == math.MaxUint64 {
			return nil, fmt.Errorf("window count overflow")
		}
	} else {
		value, err := decodePipelineJSON(event.Value)
		if err != nil {
			return nil, fmt.Errorf("%s window requires a JSON number: %w", a.kind, err)
		}
		var number float64
		switch value := value.(type) {
		case int64:
			number = float64(value)
		case uint64:
			number = float64(value)
		case float64:
			number = value
		default:
			return nil, fmt.Errorf("%s window requires a JSON number", a.kind)
		}
		if math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, fmt.Errorf("non-finite window input")
		}
		event.Value = binary.BigEndian.AppendUint64(nil, math.Float64bits(number))
	}
	result := a.Add(acc, event)
	return a.ResultChecked(result)
}
func (a pipelineWindowAggregator) MergeChecked(left, right []byte) ([]byte, error) {
	if len(left) != 8 || len(right) != 8 {
		return nil, fmt.Errorf("invalid %s accumulator", a.kind)
	}
	if a.kind == "count" && math.MaxUint64-binary.BigEndian.Uint64(left) < binary.BigEndian.Uint64(right) {
		return nil, fmt.Errorf("window count overflow")
	}
	return a.ResultChecked(a.Merge(left, right))
}
func (a pipelineWindowAggregator) ResultChecked(acc []byte) ([]byte, error) {
	if len(acc) != 8 {
		return nil, fmt.Errorf("invalid %s accumulator", a.kind)
	}
	if a.kind != "count" {
		value := math.Float64frombits(binary.BigEndian.Uint64(acc))
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, fmt.Errorf("non-finite window aggregate")
		}
	}
	return a.GetResult(acc), nil
}
func (a pipelineWindowAggregator) encode(_ context.Context, result engine.WindowResult) ([]Event, error) {
	bytes, err := a.ResultChecked(result.Value)
	if err != nil {
		return nil, err
	}
	var value any = binary.BigEndian.Uint64(bytes)
	if a.kind != "count" {
		value = math.Float64frombits(binary.BigEndian.Uint64(bytes))
	}
	payload, err := json.Marshal(map[string]any{"key": string(result.Key), a.kind: value, "window_start": result.WindowStart, "window_end": result.WindowEnd, "is_update": result.IsUpdate})
	if err != nil {
		return nil, err
	}
	return []Event{{Key: result.Key, Value: payload, EventTime: result.WindowEnd}}, nil
}
