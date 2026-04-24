package sdk

import "time"

// WindowAssigner assigns events to one or more time windows.
type WindowAssigner interface {
	// Type returns a string identifier for this window type.
	Type() string
	// Size returns the window size.
	Size() time.Duration
	// Slide returns the slide interval (only meaningful for sliding windows).
	Slide() time.Duration
	// Gap returns the session gap (only meaningful for session windows).
	Gap() time.Duration
}

// tumblingWindow assigns each event to a single non-overlapping window.
type tumblingWindow struct {
	size time.Duration
}

// TumblingWindow creates a tumbling window assigner with the given size.
func TumblingWindow(size time.Duration) WindowAssigner {
	return &tumblingWindow{size: size}
}

func (w *tumblingWindow) Type() string         { return "tumbling" }
func (w *tumblingWindow) Size() time.Duration  { return w.size }
func (w *tumblingWindow) Slide() time.Duration { return 0 }
func (w *tumblingWindow) Gap() time.Duration   { return 0 }

// slidingWindow assigns each event to overlapping windows.
type slidingWindow struct {
	size  time.Duration
	slide time.Duration
}

// SlidingWindow creates a sliding window assigner with the given size and slide interval.
func SlidingWindow(size, slide time.Duration) WindowAssigner {
	return &slidingWindow{size: size, slide: slide}
}

func (w *slidingWindow) Type() string         { return "sliding" }
func (w *slidingWindow) Size() time.Duration  { return w.size }
func (w *slidingWindow) Slide() time.Duration { return w.slide }
func (w *slidingWindow) Gap() time.Duration   { return 0 }

// sessionWindow groups events by activity with a gap timeout.
type sessionWindow struct {
	gap time.Duration
}

// SessionWindow creates a session window assigner with the given gap timeout.
func SessionWindow(gap time.Duration) WindowAssigner {
	return &sessionWindow{gap: gap}
}

func (w *sessionWindow) Type() string         { return "session" }
func (w *sessionWindow) Size() time.Duration  { return 0 }
func (w *sessionWindow) Slide() time.Duration { return 0 }
func (w *sessionWindow) Gap() time.Duration   { return w.gap }
