package sdk

import (
	"bytes"

	"github.com/tarungka/wire/internal/engine"
)

// Event is a type alias for engine.Event — zero copy, no wrapper overhead.
type Event = engine.Event

func cloneEvent(event Event) Event {
	event.Key = bytes.Clone(event.Key)
	event.Value = bytes.Clone(event.Value)
	if event.Headers != nil {
		headers := make(map[string][]byte, len(event.Headers))
		for key, value := range event.Headers {
			headers[key] = bytes.Clone(value)
		}
		event.Headers = headers
	}
	return event
}
