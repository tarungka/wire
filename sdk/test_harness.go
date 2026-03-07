package sdk

// TestHarness enables unit testing individual operator functions without
// running a full pipeline.
type TestHarness struct {
	inputs []Event
}

// NewTestHarness creates a new TestHarness.
func NewTestHarness() *TestHarness {
	return &TestHarness{}
}

// AddInput adds an event to the input queue.
func (h *TestHarness) AddInput(events ...Event) *TestHarness {
	h.inputs = append(h.inputs, events...)
	return h
}

// RunMap applies a MapFunc to all input events and returns the results.
func (h *TestHarness) RunMap(fn MapFunc) ([]Event, error) {
	var results []Event
	for _, e := range h.inputs {
		out, err := fn(e)
		if err != nil {
			return nil, err
		}
		// Check for zero event (filtered).
		if out.Key == nil && out.Value == nil && out.EventTime == 0 && out.Headers == nil {
			continue
		}
		results = append(results, out)
	}
	return results, nil
}

// RunFlatMap applies a FlatMapFunc to all input events and returns the results.
func (h *TestHarness) RunFlatMap(fn FlatMapFunc) ([]Event, error) {
	var results []Event
	for _, e := range h.inputs {
		out, err := fn(e)
		if err != nil {
			return nil, err
		}
		results = append(results, out...)
	}
	return results, nil
}

// RunFilter applies a FilterFunc to all input events and returns kept events.
func (h *TestHarness) RunFilter(fn FilterFunc) ([]Event, error) {
	var results []Event
	for _, e := range h.inputs {
		keep, err := fn(e)
		if err != nil {
			return nil, err
		}
		if keep {
			results = append(results, e)
		}
	}
	return results, nil
}

// RunProcess applies a ProcessFunc to all input events with a mock context.
func (h *TestHarness) RunProcess(fn ProcessFunc) ([]Event, error) {
	var results []Event
	for _, e := range h.inputs {
		ctx := NewMockProcessContext(e.Key)
		out, err := fn(ctx, e)
		if err != nil {
			return nil, err
		}
		results = append(results, out...)
	}
	return results, nil
}
