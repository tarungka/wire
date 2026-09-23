package rpc

import "fmt"

// WindowDefinition carries SDK window dimensions separately from factory config.
// All durations are whole milliseconds. A nil definition preserves an older
// worker factory's explicitly configured window.
type WindowDefinition struct {
	Kind            string `codec:"kind"`
	Size            int64  `codec:"size"`
	Slide           int64  `codec:"slide"`
	Gap             int64  `codec:"gap"`
	AllowedLateness int64  `codec:"allowed_lateness"`
}

func (w WindowDefinition) Validate() error {
	if w.Size < 0 || w.Slide < 0 || w.Gap < 0 || w.AllowedLateness < 0 {
		return fmt.Errorf("window: durations must be nonnegative milliseconds")
	}
	switch w.Kind {
	case "tumbling":
		if w.Size == 0 {
			return fmt.Errorf("window: positive size required")
		}
	case "sliding":
		if w.Size == 0 || w.Slide == 0 {
			return fmt.Errorf("window: positive size and slide required")
		}
	case "session":
		if w.Gap == 0 {
			return fmt.Errorf("window: positive gap required")
		}
	default:
		return fmt.Errorf("window: unknown kind %q", w.Kind)
	}
	return nil
}
