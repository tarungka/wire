package rocksdb

import "errors"

var (
	// ErrStoreNotOpen is returned when a Store is not open.
	ErrDBNotOpen = errors.New("db not open")

	// ErrStoreOpen is returned when a Store is already open.
	ErrDBOpen = errors.New("db already open")

	// ErrNotReady is returned when a Store is not ready to accept requests.
	ErrNotReady = errors.New("db not ready")

	// ErrNotLeader is returned when a node attempts to execute a leader-only
	// operation.
	ErrNotLeader = errors.New("db not leader")

	// ErrStaleRead is returned if the executing the query would violate the
	// requested freshness.
	ErrStaleRead = errors.New("db stale read")

	// ErrWaitForLeaderTimeout is returned when the Store cannot determine the leader
	// within the specified time.
	ErrWaitForLeaderTimeout = errors.New("db timeout waiting for leader")

	// ErrInvalidBackupFormat is returned when the requested backup format
	// is not valid.
	ErrInvalidBackupFormat = errors.New("db invalid backup format")

	// ErrLoadInProgress is returned when a load is already in progress and the
	// requested operation cannot be performed.
	ErrLoadInProgress = errors.New("db load in progress")

	// ErrNotImplemented when there is no implementation of the function
	// will only exits until this application in under development
	ErrNotImplemented = errors.New("not implemented")
)
