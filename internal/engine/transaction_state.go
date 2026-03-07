package engine

// TxnState represents the current phase of a transactional sink's transaction.
type TxnState uint8

const (
	TxnActive       TxnState = iota // Transaction open; accepting writes.
	TxnPreCommitted                 // PreCommit succeeded; waiting for global completion.
	TxnCommitted                    // Commit succeeded; transaction finalized.
)

// String returns a human-readable representation of TxnState.
func (s TxnState) String() string {
	switch s {
	case TxnActive:
		return "ACTIVE"
	case TxnPreCommitted:
		return "PRE_COMMITTED"
	case TxnCommitted:
		return "COMMITTED"
	default:
		return "UNKNOWN"
	}
}

// SinkTaskTxnState tracks the two-phase commit state for a single
// transactional sink task.
type SinkTaskTxnState struct {
	TaskID                  string
	CurrentCheckpoint       uint64
	LastCommittedCheckpoint uint64
	State                   TxnState
}
