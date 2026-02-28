package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildJSONLPipeline_RunsToCompletion(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.jsonl")
	outputPath := filepath.Join(dir, "output.jsonl")

	// Generate 20 events: 5 per window over 4 windows (each 5 minutes).
	// Events at 1-min intervals starting at 10:00.
	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	f, err := os.Create(inputPath)
	if err != nil {
		t.Fatalf("create input: %v", err)
	}

	var expectedRevenue [4]float64
	var expectedProfit [4]float64
	var expectedCounts [4]int

	for i := 0; i < 20; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		revenue := 100.0 + float64(i)*10.0
		profit := 20.0 + float64(i)*5.0
		windowIdx := i / 5

		expectedRevenue[windowIdx] += revenue
		expectedProfit[windowIdx] += profit
		expectedCounts[windowIdx]++

		line := fmt.Sprintf(`{"timestamp":"%s","revenue":%.2f,"profit":%.2f}`,
			ts.Format(time.RFC3339), revenue, profit)
		fmt.Fprintln(f, line)
	}
	f.Close()

	var stdoutBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pl, err := BuildJSONLPipeline(ctx, JSONLPipelineConfig{
		InputPath:  inputPath,
		OutputPath: outputPath,
		WindowSize: 5 * time.Minute,
		Stdout:     &stdoutBuf,
	})
	if err != nil {
		t.Fatalf("BuildJSONLPipeline: %v", err)
	}

	if err := pl.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify stdout output.
	stdoutLines := strings.Split(strings.TrimSpace(stdoutBuf.String()), "\n")
	if len(stdoutLines) != 4 {
		t.Fatalf("expected 4 stdout lines, got %d:\n%s", len(stdoutLines), stdoutBuf.String())
	}

	// Verify JSONL output file.
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	fileLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(fileLines) != 4 {
		t.Fatalf("expected 4 file lines, got %d:\n%s", len(fileLines), string(data))
	}

	type windowResult struct {
		WindowStart  string  `json:"window_start"`
		WindowEnd    string  `json:"window_end"`
		TotalRevenue float64 `json:"total_revenue"`
		TotalProfit  float64 `json:"total_profit"`
		EventCount   int     `json:"event_count"`
	}

	for i, line := range fileLines {
		var result windowResult
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			t.Fatalf("unmarshal line %d: %v", i, err)
		}
		if result.TotalRevenue != expectedRevenue[i] {
			t.Errorf("window %d revenue: got %f, want %f", i, result.TotalRevenue, expectedRevenue[i])
		}
		if result.TotalProfit != expectedProfit[i] {
			t.Errorf("window %d profit: got %f, want %f", i, result.TotalProfit, expectedProfit[i])
		}
		if result.EventCount != expectedCounts[i] {
			t.Errorf("window %d count: got %d, want %d", i, result.EventCount, expectedCounts[i])
		}
	}
}
