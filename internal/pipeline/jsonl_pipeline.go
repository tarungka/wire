package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/operators"
	"github.com/tarungka/wire/internal/transport"
)

// JSONLPipelineConfig configures the JSONL aggregation pipeline.
type JSONLPipelineConfig struct {
	InputPath  string
	OutputPath string
	WindowSize time.Duration
	Stdout     io.Writer // injectable for testing; nil defaults to os.Stdout
}

// BuildJSONLPipeline creates a 2-slot pipeline:
//
//	Slot 0 (source): JSONLSource → edge1
//	Slot 1 (sink):   edge1 → TumblingWindowSink
//
// The sink writes aggregated results to both Stdout and the output JSONL file.
func BuildJSONLPipeline(ctx context.Context, pcfg JSONLPipelineConfig) (*Pipeline, error) {
	if pcfg.Stdout == nil {
		pcfg.Stdout = os.Stdout
	}

	cfg := engine.DefaultTaskSlotConfig()

	edge1, err := newLoopbackEdge(ctx)
	if err != nil {
		return nil, fmt.Errorf("jsonl pipeline: edge1: %w", err)
	}

	// Slot 0: JSONLSource → edge1
	source := operators.NewJSONLSource(pcfg.InputPath)
	slot0 := engine.NewTaskSlot(
		cfg,
		nil,                                    // no input streams
		[]*transport.FrameStream{edge1.Writer}, // output to edge1
		nil,                                    // no operators
		source,
	)

	// Slot 1: edge1 → TumblingWindowSink (terminal)
	sink := operators.NewTumblingWindowSink(pcfg.WindowSize, pcfg.Stdout, pcfg.OutputPath)
	slot1 := engine.NewTaskSlot(
		cfg,
		[]*transport.FrameStream{edge1.Reader}, // input from edge1
		nil,                                    // no outputs (terminal)
		[]engine.Operator{sink},
		nil, // not a source
	)

	return &Pipeline{
		Slots: []*engine.TaskSlot{slot0, slot1},
		Edges: []*LoopbackEdge{edge1},
	}, nil
}
