package operators

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

// windowAccumulator holds the running aggregation state for a single window.
type windowAccumulator struct {
	TotalRevenue float64
	TotalProfit  float64
	EventCount   int
}

// windowOutput is the JSONL output format for a completed window.
type windowOutput struct {
	WindowStart  string  `json:"window_start"`
	WindowEnd    string  `json:"window_end"`
	TotalRevenue float64 `json:"total_revenue"`
	TotalProfit  float64 `json:"total_profit"`
	EventCount   int     `json:"event_count"`
}

// TumblingWindowSink aggregates revenue and profit per tumbling time window.
// It writes completed windows to both an io.Writer (human-readable) and a
// JSONL output file.
// Implements engine.SinkOperator.
type TumblingWindowSink struct {
	windowSizeMs      int64
	stdout            io.Writer
	outputPath        string
	outputFile        *os.File
	windows           map[int64]*windowAccumulator
	latestWindowStart int64
}

// NewTumblingWindowSink creates a sink that aggregates into windows of the
// given size. Results are written to w (human-readable) and to outputPath (JSONL).
// If w is nil, os.Stdout is used.
func NewTumblingWindowSink(windowSize time.Duration, w io.Writer, outputPath string) *TumblingWindowSink {
	if w == nil {
		w = os.Stdout
	}
	return &TumblingWindowSink{
		windowSizeMs: windowSize.Milliseconds(),
		stdout:       w,
		outputPath:   outputPath,
		windows:      make(map[int64]*windowAccumulator),
	}
}

func (s *TumblingWindowSink) Open(ctx context.Context) error {
	f, err := os.Create(s.outputPath)
	if err != nil {
		return fmt.Errorf("tumbling window sink: create output %q: %w", s.outputPath, err)
	}
	s.outputFile = f
	return nil
}

func (s *TumblingWindowSink) Close() error {
	// Flush all remaining open windows sorted by start time.
	starts := make([]int64, 0, len(s.windows))
	for k := range s.windows {
		starts = append(starts, k)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })

	for _, ws := range starts {
		if err := s.flushWindow(ws, s.windows[ws]); err != nil {
			s.outputFile.Close()
			return err
		}
	}
	s.windows = nil

	if s.outputFile != nil {
		return s.outputFile.Close()
	}
	return nil
}

func (s *TumblingWindowSink) Checkpoint(id uint64) ([]byte, error) { return nil, nil }

func (s *TumblingWindowSink) Write(ctx context.Context, event engine.Event) error {
	// Decode the revenue and profit from the raw JSON.
	var rec struct {
		Revenue float64 `json:"revenue"`
		Profit  float64 `json:"profit"`
	}
	if err := json.Unmarshal(event.Value, &rec); err != nil {
		return fmt.Errorf("tumbling window sink: unmarshal event: %w", err)
	}

	// Determine window boundaries.
	windowStart := event.EventTime - (event.EventTime % s.windowSizeMs)

	// Accumulate into the window.
	acc, ok := s.windows[windowStart]
	if !ok {
		acc = &windowAccumulator{}
		s.windows[windowStart] = acc
	}
	acc.TotalRevenue += rec.Revenue
	acc.TotalProfit += rec.Profit
	acc.EventCount++

	// Flush completed windows: any window whose end <= current window start.
	if windowStart > s.latestWindowStart {
		if err := s.flushCompleted(windowStart); err != nil {
			return err
		}
		s.latestWindowStart = windowStart
	}

	return nil
}

// flushCompleted flushes all windows whose end time <= newWindowStart.
func (s *TumblingWindowSink) flushCompleted(newWindowStart int64) error {
	starts := make([]int64, 0, len(s.windows))
	for k := range s.windows {
		starts = append(starts, k)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })

	for _, ws := range starts {
		windowEnd := ws + s.windowSizeMs
		if windowEnd <= newWindowStart {
			if err := s.flushWindow(ws, s.windows[ws]); err != nil {
				return err
			}
			delete(s.windows, ws)
		}
	}
	return nil
}

// flushWindow writes a completed window to both stdout and the output file.
func (s *TumblingWindowSink) flushWindow(windowStart int64, acc *windowAccumulator) error {
	windowEnd := windowStart + s.windowSizeMs
	startStr := time.UnixMilli(windowStart).UTC().Format(time.RFC3339)
	endStr := time.UnixMilli(windowEnd).UTC().Format(time.RFC3339)

	// Human-readable stdout line.
	_, err := fmt.Fprintf(s.stdout, "[%s — %s] revenue=%.2f  profit=%.2f  events=%d\n",
		startStr, endStr, acc.TotalRevenue, acc.TotalProfit, acc.EventCount)
	if err != nil {
		return fmt.Errorf("tumbling window sink: write stdout: %w", err)
	}

	// JSONL output line.
	out := windowOutput{
		WindowStart:  startStr,
		WindowEnd:    endStr,
		TotalRevenue: acc.TotalRevenue,
		TotalProfit:  acc.TotalProfit,
		EventCount:   acc.EventCount,
	}
	data, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("tumbling window sink: marshal output: %w", err)
	}
	if _, err := fmt.Fprintf(s.outputFile, "%s\n", data); err != nil {
		return fmt.Errorf("tumbling window sink: write file: %w", err)
	}
	return nil
}
