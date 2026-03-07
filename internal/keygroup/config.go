package keygroup

// Default key group configuration values per WIP-03 Section 2.1.
const (
	DefaultNumKeyGroups = 128
	MinKeyGroups        = 1
	MaxKeyGroups        = 32768
)

// Config holds key group assignment configuration.
type Config struct {
	NumKeyGroups int // Must be power of 2, in [1, 32768].
	Parallelism  int // Number of parallel task instances.
}

// DefaultConfig returns a Config populated with default values.
func DefaultConfig() Config {
	return Config{
		NumKeyGroups: DefaultNumKeyGroups,
		Parallelism:  1,
	}
}

// Validate checks that the configuration is valid.
func (c Config) Validate() error {
	if c.NumKeyGroups < MinKeyGroups || c.NumKeyGroups > MaxKeyGroups {
		return ErrKeyGroupsOutOfRange
	}
	if c.NumKeyGroups&(c.NumKeyGroups-1) != 0 {
		return ErrNotPowerOfTwo
	}
	if c.Parallelism < 1 {
		return ErrInvalidParallelism
	}
	if c.Parallelism > c.NumKeyGroups {
		return ErrParallelismExceedsKeyGroups
	}
	return nil
}
