package partitioner

import (
	"context"

	"github.com/rs/zerolog/log"
)

type Partitoner[T any] struct {
	// Number of partitions
	partitions int
	// Hashing function
	hashFn func(T) (uint64, error)
	// Buffer size
	bufferSize int // Buffer size for partitioned channels
	// Max retires
	maxRetries int // Retry attempts for error handling
	// Context
	ctx context.Context // Context for cancellation/timeouts
}

type PartitonerOption[T any] func(*Partitoner[T])

// TODO: impl wg to ensure no loss of data
// WARNING!: There can be loss of data if the service starts to shutdown
func (p *Partitoner[T]) PartitionData(dataChannel <-chan T) []chan T {
	partitionedChannels := make([]chan T, p.partitions)

	for i := 0; i < p.partitions; i++ {
		partitionedChannels[i] = make(chan T)
	}

	go func() {
		defer func() {
			for _, ch := range partitionedChannels {
				close(ch)
			}
		}()

		for data := range dataChannel {
			// Simple round-robin partitioning
			hashedValue, err := p.hashFn(data)
			if err != nil {
				log.Err(err).Msg("Error when hashing the job")
			}
			partition := hashedValue % uint64(p.partitions)
			partitionedChannels[partition] <- data
		}
	}()

	return partitionedChannels
}


func WithBufferSize[T any](size int) PartitonerOption[T] {
	return func(p *Partitoner[T]) {
		p.bufferSize = size
	}
}

func WithMaxRetries[T any](retries int) PartitonerOption[T] {
	return func(p *Partitoner[T]) {
		p.maxRetries = retries
	}
}

func WithContext[T any](ctx context.Context) PartitonerOption[T] {
	return func(p *Partitoner[T]) {
		p.ctx = ctx
	}
}


// Partitoner factory function
func NewPartitoner[T any](partitions int, hashFn func(T) (uint64, error), opts ...PartitonerOption[T]) *Partitoner[T] {

	// Create a default partitioner with basic values
	p := &Partitoner[T]{
		partitions:   partitions,
		hashFn:     hashFn,
		bufferSize: 100,                  // Default buffer size
		maxRetries: 3,                    // Default retries
		ctx:        context.Background(), // Default context
	}

	// Apply any optional configurations
	for _, opt := range opts {
		opt(p)
	}

	return p
}
