package engine

import (
	"fmt"
	"strconv"
)

const (
	WindowStartHeader  = "wire.window.start"
	WindowEndHeader    = "wire.window.end"
	WindowUpdateHeader = "wire.window.update"
)

// WithWindowResultMetadata preserves an encoder's payload and attaches the
// event-time window identity. Headers survive ordinary data transport framing.
func WithWindowResultMetadata(event Event, result WindowResult) Event {
	headers := make(map[string][]byte, len(event.Headers)+3)
	for key, value := range event.Headers {
		headers[key] = value
	}
	headers[WindowStartHeader] = []byte(strconv.FormatInt(result.WindowStart, 10))
	headers[WindowEndHeader] = []byte(strconv.FormatInt(result.WindowEnd, 10))
	headers[WindowUpdateHeader] = []byte(strconv.FormatBool(result.IsUpdate))
	event.Headers = headers
	return event
}

// DecodeWindowResult returns a result and whether the event carries window
// metadata. Partial or malformed metadata returns an error rather than silently
// presenting an updated result as an initial one.
func DecodeWindowResult(event Event) (WindowResult, bool, error) {
	start, a := event.Headers[WindowStartHeader]
	end, b := event.Headers[WindowEndHeader]
	update, c := event.Headers[WindowUpdateHeader]
	if !a && !b && !c {
		return WindowResult{}, false, nil
	}
	if !a || !b || !c {
		return WindowResult{}, true, fmt.Errorf("window: incomplete result metadata")
	}
	s, err := strconv.ParseInt(string(start), 10, 64)
	if err != nil {
		return WindowResult{}, true, fmt.Errorf("window: invalid start: %w", err)
	}
	e, err := strconv.ParseInt(string(end), 10, 64)
	if err != nil || e <= s {
		return WindowResult{}, true, fmt.Errorf("window: invalid end %q", end)
	}
	u, err := strconv.ParseBool(string(update))
	if err != nil {
		return WindowResult{}, true, fmt.Errorf("window: invalid update flag: %w", err)
	}
	return WindowResult{Key: event.Key, Value: event.Value, WindowStart: s, WindowEnd: e, IsUpdate: u}, true, nil
}
