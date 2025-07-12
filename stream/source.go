package stream

import (
	"context"
)

// Source is an interface for data sources.
type Source interface {
	// Open opens the source.
	Open(ctx context.Context) (<-chan Event, error)
	// Close closes the source.
	Close() error
}

// NumberSource is a simple source that generates a stream of numbers.
type NumberSource struct {
	count int
}

// NewNumberSource creates a new NumberSource.
func NewNumberSource(count int) *NumberSource {
	return &NumberSource{
		count: count,
	}
}

// Open opens the source.
func (s *NumberSource) Open(ctx context.Context) (<-chan Event, error) {
	out := make(chan Event)
	go func() {
		defer close(out)
		for i := 0; i < s.count; i++ {
			select {
			case out <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Close closes the source.
func (s *NumberSource) Close() error {
	return nil
}
