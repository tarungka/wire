package stream

import (
	"context"
	"fmt"
)

// Sink is an interface for data sinks.
type Sink interface {
	// Open opens the sink.
	Open(ctx context.Context) (chan<- Event, error)
	// Close closes the sink.
	Close() error
}

// PrintSink is a simple sink that prints events to the console.
type PrintSink struct{}

// NewPrintSink creates a new PrintSink.
func NewPrintSink() *PrintSink {
	return &PrintSink{}
}

// Open opens the sink.
func (s *PrintSink) Open(ctx context.Context) (chan<- Event, error) {
	in := make(chan Event)
	go func() {
		for event := range in {
			fmt.Printf("Sink: %v\n", event)
		}
	}()
	return in, nil
}

// Close closes the sink.
func (s *PrintSink) Close() error {
	return nil
}
