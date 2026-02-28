package pipeline

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/operators"
	"github.com/tarungka/wire/internal/transport"
)

func TestBuildDemoPipeline_RunsToCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	eventCount := 10

	// Build a pipeline that writes to a buffer instead of stdout.
	// We replicate BuildDemoPipeline but swap the StdoutSink writer.
	var buf bytes.Buffer

	cfg := engine.DefaultTaskSlotConfig()

	edge1, err := newLoopbackEdge(ctx)
	if err != nil {
		t.Fatalf("edge1: %v", err)
	}

	edge2, err := newLoopbackEdge(ctx)
	if err != nil {
		edge1.Close()
		t.Fatalf("edge2: %v", err)
	}

	source := operators.NewGeneratorSource(eventCount)
	slot0 := engine.NewTaskSlot(
		cfg, nil,
		[]*transport.FrameStream{edge1.Writer},
		nil, source,
	)

	slot1 := engine.NewTaskSlot(
		cfg,
		[]*transport.FrameStream{edge1.Reader},
		[]*transport.FrameStream{edge2.Writer},
		[]engine.Operator{&operators.ToUpperMap{}},
		nil,
	)

	slot2 := engine.NewTaskSlot(
		cfg,
		[]*transport.FrameStream{edge2.Reader},
		nil,
		[]engine.Operator{operators.NewStdoutSink(&buf)},
		nil,
	)

	pl := &Pipeline{
		Slots: []*engine.TaskSlot{slot0, slot1, slot2},
		Edges: []*LoopbackEdge{edge1, edge2},
	}

	if err := pl.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != eventCount {
		t.Fatalf("got %d lines, want %d\noutput:\n%s", len(lines), eventCount, output)
	}

	for _, line := range lines {
		if !strings.Contains(line, "EVENT-") {
			t.Errorf("expected uppercased event in line, got: %q", line)
		}
	}
}
