package engine

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
)

// DLQSink deliberately excludes checkpoint/transaction methods. Side output is
// best effort and must never commit or abort the main sink's transactions.
type DLQSink interface {
	Open(context.Context) error
	Write(context.Context, Event) error
	Close() error
}

// DLQDestination isolates lifecycle failures from the main operator chain.
// Its owner opens it before processing and closes it after all writers finish.
// A failed Open fails startup, after closing any partially allocated resources.
type DLQDestination struct {
	sink DLQSink
	log  zerolog.Logger
}

func OpenDLQDestination(ctx context.Context, sink DLQSink, log zerolog.Logger) (*DLQDestination, error) {
	if _, ok := sink.(TransactionalSink); ok {
		return nil, fmt.Errorf("transactional sinks cannot be used as DLQ destinations")
	}
	destination := &DLQDestination{sink: sink, log: log}
	if err := safeInvoke(func() error { return sink.Open(ctx) }); err != nil {
		destination.Close()
		return nil, fmt.Errorf("DLQ sink open: %w", err)
	}
	return destination, nil
}

func (d *DLQDestination) Write(ctx context.Context, event DLQEvent) error {
	data, err := MarshalDLQEvent(event)
	if err != nil {
		return err
	}
	return d.sink.Write(ctx, Event{Key: event.OriginalEvent.Key, Value: data, EventTime: event.Timestamp})
}

func (d *DLQDestination) Close() {
	// Close even after failed Open so partially allocated resources can be freed.
	if err := safeInvoke(d.sink.Close); err != nil {
		d.log.Error().Err(err).Msg("DLQ sink failed to close")
	}
}
