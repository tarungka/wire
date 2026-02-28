package operators

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

// StdoutSink writes each event to an io.Writer in a human-readable format.
// Implements engine.SinkOperator.
type StdoutSink struct {
	w io.Writer
}

// NewStdoutSink creates a sink that writes to w. If w is nil, os.Stdout is used.
func NewStdoutSink(w io.Writer) *StdoutSink {
	if w == nil {
		w = os.Stdout
	}
	return &StdoutSink{w: w}
}

func (s *StdoutSink) Open(ctx context.Context) error       { return nil }
func (s *StdoutSink) Close() error                         { return nil }
func (s *StdoutSink) Checkpoint(id uint64) ([]byte, error) { return nil, nil }

func (s *StdoutSink) Write(ctx context.Context, e engine.Event) error {
	ts := time.UnixMilli(e.EventTime).UTC().Format(time.RFC3339Nano)
	_, err := fmt.Fprintf(s.w, "[%s] key=%-6s value=%s\n", ts, string(e.Key), string(e.Value))
	return err
}
