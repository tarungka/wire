package operators

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tarungka/wire/internal/engine"
)

func TestStdoutSink_WritesFormatted(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()
	s := NewStdoutSink(&buf)

	if err := s.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	e := engine.Event{
		Key:       []byte("0"),
		Value:     []byte("EVENT-0"),
		EventTime: 1709136000123, // 2024-02-28T12:00:00.123Z
	}

	if err := s.Write(ctx, e); err != nil {
		t.Fatalf("Write: %v", err)
	}

	line := buf.String()
	if !strings.Contains(line, "key=0") {
		t.Errorf("expected key=0 in output, got: %q", line)
	}
	if !strings.Contains(line, "value=EVENT-0") {
		t.Errorf("expected value=EVENT-0 in output, got: %q", line)
	}
	if !strings.HasPrefix(line, "[") {
		t.Errorf("expected line to start with '[', got: %q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("expected line to end with newline, got: %q", line)
	}
}

func TestStdoutSink_DefaultsToStdout(t *testing.T) {
	s := NewStdoutSink(nil)
	if s.w == nil {
		t.Fatal("expected non-nil writer when passing nil")
	}
}
