package engine

import (
	"context"

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
// A failed Open disables delivery for this run; each attempted delivery then
// reaches the usual error handler's drop counter and log.
type DLQDestination struct {
	sink    DLQSink
	openErr error
	log     zerolog.Logger
}

func OpenDLQDestination(ctx context.Context, sink DLQSink, log zerolog.Logger) *DLQDestination {
	destination := &DLQDestination{sink: sink, log: log}
	destination.openErr = safeInvoke(func() error { return sink.Open(ctx) })
	if destination.openErr != nil {
		log.Error().Err(destination.openErr).Msg("DLQ sink failed to open; delivery disabled for this run")
	}
	return destination
}

func (d *DLQDestination) Write(ctx context.Context, event DLQEvent) error {
	if d.openErr != nil {
		return d.openErr
	}
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
