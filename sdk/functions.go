package sdk

// MapFunc transforms a single event into a single output event.
type MapFunc func(Event) (Event, error)

// FlatMapFunc transforms a single event into zero or more output events.
type FlatMapFunc func(Event) ([]Event, error)

// FilterFunc returns true if the event should be kept, false to drop it.
type FilterFunc func(Event) (bool, error)

// KeySelector extracts a key from an event for partitioning.
type KeySelector func(Event) ([]byte, error)

// TimestampExtractor extracts an event-time timestamp from an event.
type TimestampExtractor func(Event) int64

// ProcessFunc processes events with access to keyed state and context.
type ProcessFunc func(ctx ProcessContext, event Event) ([]Event, error)

// ReduceFunc combines two events into one (associative, commutative).
type ReduceFunc func(a, b Event) (Event, error)

// WindowFunc applies a function to all events in a window.
type WindowFunc func(info WindowInfo, events []Event) ([]Event, error)

// WindowInfo provides metadata about the window being processed.
type WindowInfo struct {
	Start int64 // Window start timestamp (millis).
	End   int64 // Window end timestamp (millis).
}
