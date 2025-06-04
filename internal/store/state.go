package store

import "time"

// IsStaleRead returns whether a read is stale.
func IsStaleRead(
	leaderLastContact time.Time,
	lastFSMUpdateTime time.Time,
	lastAppendedAtTime time.Time,
	fsmIndex uint64,
	commitIndex uint64,
	freshness int64,
	strict bool,
) bool {
	if freshness == 0 {
		// Freshness not set, so no read can be stale.
		return false
	}
	if time.Since(leaderLastContact).Nanoseconds() > freshness {
		// The Leader has not been in contact within the freshness window, so
		// the read is stale.
		return true
	}
	if !strict {
		// Strict mode is not enabled, so no further checks are needed.
		return false
	}
	if lastAppendedAtTime.IsZero() {
		// We've yet to be told about any appended log entries, so we
		// assume we're caught up.
		return false
	}
	if fsmIndex == commitIndex {
		// FSM index is the same as the commit index, so we're caught up.
		return false
	}
	// We're not caught up. So was the log that last updated our local FSM
	// appended by the Leader to its log within the freshness window?
	return lastFSMUpdateTime.Sub(lastAppendedAtTime).Nanoseconds() > freshness
}
