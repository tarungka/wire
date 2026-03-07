package coordinator

import "errors"

var (
	// ErrStaleEpoch indicates the worker's epoch is newer than the coordinator's,
	// meaning the coordinator is stale and should not be serving.
	ErrStaleEpoch = errors.New("coordinator: stale epoch")

	// ErrNoLeader indicates no leader has been elected yet.
	ErrNoLeader = errors.New("coordinator: no leader elected")

	// ErrNotLeader indicates the current node is not the leader
	// and cannot serve write requests.
	ErrNotLeader = errors.New("coordinator: not the leader")

	// ErrLeadershipLost indicates the coordinator lost its leadership
	// (e.g. lease expired, election lost).
	ErrLeadershipLost = errors.New("coordinator: leadership lost")

	// ErrStoreCorrupted indicates the metadata store contains
	// invalid or inconsistent data that prevents recovery.
	ErrStoreCorrupted = errors.New("coordinator: metadata store corrupted")

	// ErrWorkerNotFound indicates the specified worker ID does not exist
	// in the coordinator's registry.
	ErrWorkerNotFound = errors.New("coordinator: worker not found")

	// ErrJobNotFound indicates the specified job ID does not exist
	// in the coordinator's job registry.
	ErrJobNotFound = errors.New("coordinator: job not found")

	// ErrRecoveryFailed indicates the coordinator could not recover
	// its state from the metadata store after a restart or failover.
	ErrRecoveryFailed = errors.New("coordinator: recovery failed")

	// ErrEpochMismatch indicates the worker's reported epoch does not match
	// the coordinator's current epoch during re-registration.
	ErrEpochMismatch = errors.New("coordinator: epoch mismatch on re-registration")

	// ErrInvalidTransition indicates an illegal job state transition was attempted.
	ErrInvalidTransition = errors.New("coordinator: invalid state transition")

	// ErrJobNotRunning indicates the job must be in RUNNING state for this operation.
	ErrJobNotRunning = errors.New("coordinator: job is not running")

	// ErrJobNotPaused indicates the job must be in PAUSED state for this operation.
	ErrJobNotPaused = errors.New("coordinator: job is not paused")

	// ErrSavepointNotFound indicates the specified savepoint ID does not exist.
	ErrSavepointNotFound = errors.New("coordinator: savepoint not found")

	// ErrInvalidConfig indicates the job configuration is invalid.
	ErrInvalidConfig = errors.New("coordinator: invalid job configuration")

	// ErrJobExists indicates a job with the same name already exists and is active.
	ErrJobExists = errors.New("coordinator: job already exists")
)
