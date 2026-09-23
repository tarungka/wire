package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func embeddedWindow(node *StreamNode) (engine.Operator, error) {
	if node.Window == nil {
		return nil, fmt.Errorf("sdk: event-time window requires an assigner")
	}
	for _, duration := range []time.Duration{node.Window.Size(), node.Window.Slide(), node.Window.Gap()} {
		if duration < 0 || duration%time.Millisecond != 0 {
			return nil, fmt.Errorf("sdk: window dimensions require nonnegative whole milliseconds")
		}
	}
	config := engine.WindowConfig{Kind: node.Window.Type(), Size: node.Window.Size().Milliseconds(), Slide: node.Window.Slide().Milliseconds(), Gap: node.Window.Gap().Milliseconds(), AllowedLateness: node.AllowedLateness, AggregationID: fmt.Sprintf("sdk-node-%d", node.ID)}
	var aggregator engine.WindowAggregator = node.Aggregator
	switch {
	case node.ReduceFn != nil:
		aggregator = reduceWindowAggregator{fn: node.ReduceFn}
	case node.WindowFn != nil:
		aggregator = applyWindowAggregator{}
	}
	op, err := engine.NewEventTimeWindowOperator(config, aggregator, func(result engine.WindowResult) engine.Event {
		return engine.Event{Key: result.Key, Value: result.Value, EventTime: result.WindowEnd}
	})
	if err != nil {
		return nil, err
	}
	op.LateOutputTag = node.LateOutputTag
	if node.ReduceFn != nil {
		op.ResultEvents = func(_ context.Context, r engine.WindowResult) ([]Event, error) {
			var event Event
			if err := json.Unmarshal(r.Value, &event); err != nil {
				return nil, err
			}
			event.Key, event.EventTime = r.Key, r.WindowEnd
			return []Event{event}, nil
		}
	}
	if node.WindowFn != nil {
		op.ResultEvents = func(_ context.Context, r engine.WindowResult) ([]Event, error) {
			decoder := json.NewDecoder(bytes.NewReader(r.Value))
			var records []Event
			for {
				var record Event
				err := decoder.Decode(&record)
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
				records = append(records, record)
			}
			events, err := node.WindowFn(WindowInfo{Start: r.WindowStart, End: r.WindowEnd, IsUpdate: r.IsUpdate}, records)
			if err != nil {
				return nil, err
			}
			for i := range events {
				events[i].Key = bytes.Clone(r.Key)
				events[i].EventTime = r.WindowEnd
			}
			return events, nil
		}
	}
	return op, nil
}

func windowDefinition(node *StreamNode) (*rpc.WindowDefinition, error) {
	if node.Window == nil {
		return nil, fmt.Errorf("sdk: window assigner is required")
	}
	for _, duration := range []time.Duration{node.Window.Size(), node.Window.Slide(), node.Window.Gap()} {
		if duration < 0 || duration%time.Millisecond != 0 {
			return nil, fmt.Errorf("sdk: window dimensions require whole milliseconds")
		}
	}
	definition := &rpc.WindowDefinition{Kind: node.Window.Type(), Size: node.Window.Size().Milliseconds(), Slide: node.Window.Slide().Milliseconds(), Gap: node.Window.Gap().Milliseconds(), AllowedLateness: node.AllowedLateness}
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	return definition, nil
}
