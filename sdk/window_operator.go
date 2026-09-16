package sdk

import (
	"fmt"

	"github.com/tarungka/wire/internal/engine"
)

func embeddedWindow(node *StreamNode) (engine.Operator, error) {
	if node.Window == nil || node.Aggregator == nil {
		return nil, fmt.Errorf("sdk: event-time window requires an assigner and Aggregator")
	}
	config := engine.WindowConfig{Kind: node.Window.Type(), Size: node.Window.Size().Milliseconds(), Slide: node.Window.Slide().Milliseconds(), Gap: node.Window.Gap().Milliseconds(), AllowedLateness: node.AllowedLateness, AggregationID: fmt.Sprintf("sdk-node-%d", node.ID)}
	return engine.NewEventTimeWindowOperator(config, node.Aggregator, func(result engine.WindowResult) engine.Event {
		return engine.Event{Key: result.Key, Value: result.Value, EventTime: result.WindowEnd}
	})
}
