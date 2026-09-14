// Package checkpointpolicy defines the bounded checkpoint failure-rate policy.
package checkpointpolicy

const WindowSize = 100

// Record returns a fresh window so a failed metadata write cannot mutate the
// previously persisted policy through a shared slice backing array.
func Record(previous []bool, failed bool) []bool {
	if len(previous) >= WindowSize {
		previous = previous[len(previous)-WindowSize+1:]
	}
	next := append(make([]bool, 0, len(previous)+1), previous...)
	return append(next, failed)
}

// Exceeded enforces a rate only after a full window of terminal outcomes.
func Exceeded(outcomes []bool, tolerance float64) bool {
	if tolerance <= 0 || len(outcomes) < WindowSize {
		return false
	}
	failures := 0
	for _, failed := range outcomes {
		if failed {
			failures++
		}
	}
	return float64(failures)/float64(len(outcomes)) > tolerance
}
