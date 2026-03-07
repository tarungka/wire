package keygroup

import "errors"

var (
	// ErrParallelismExceedsKeyGroups is returned when parallelism exceeds the number of key groups.
	ErrParallelismExceedsKeyGroups = errors.New("keygroup: parallelism cannot exceed key_groups")

	// ErrInvalidParallelism is returned when parallelism is less than 1.
	ErrInvalidParallelism = errors.New("keygroup: parallelism must be >= 1")

	// ErrNotPowerOfTwo is returned when the number of key groups is not a power of 2.
	ErrNotPowerOfTwo = errors.New("keygroup: key_groups must be a power of 2")

	// ErrKeyGroupsOutOfRange is returned when key groups is outside [1, 32768].
	ErrKeyGroupsOutOfRange = errors.New("keygroup: key_groups must be in range [1, 32768]")

	// ErrKeyGroupsMismatch is returned when the key group count doesn't match a savepoint.
	ErrKeyGroupsMismatch = errors.New("keygroup: key group count mismatch with savepoint")

	// ErrInvalidTaskIndex is returned when a task index is out of range.
	ErrInvalidTaskIndex = errors.New("keygroup: task index out of range")

	// ErrPebbleKeyTooShort is returned when a Pebble key is shorter than MinPebbleKeySize.
	ErrPebbleKeyTooShort = errors.New("keygroup: pebble key too short")
)
