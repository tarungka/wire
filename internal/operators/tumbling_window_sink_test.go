package operators

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

func makeEvent(ts time.Time, revenue, profit float64) engine.Event {
	val, _ := json.Marshal(map[string]any{
		"timestamp": ts.Format(time.RFC3339),
		"revenue":   revenue,
		"profit":    profit,
	})
	return engine.Event{
		Key:       []byte(ts.Format(time.RFC3339)),
		Value:     val,
		EventTime: ts.UnixMilli(),
	}
}

func newTestSink(t *testing.T, windowSize time.Duration, buf *bytes.Buffer) (*TumblingWindowSink, string) {
	t.Helper()
	dir := t.TempDir()
	outPath := filepath.Join(dir, "output.jsonl")
	sink := NewTumblingWindowSink(windowSize, buf, outPath)
	return sink, outPath
}

func TestTumblingWindowSink_SingleWindow(t *testing.T) {
	var buf bytes.Buffer
	sink, outPath := newTestSink(t, 5*time.Minute, &buf)

	ctx := context.Background()
	if err := sink.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// All events within the same 5-min window [10:00, 10:05).
	events := []engine.Event{
		makeEvent(base, 100, 30),
		makeEvent(base.Add(1*time.Minute), 200, 60),
		makeEvent(base.Add(2*time.Minute), 150, 45),
	}

	for _, e := range events {
		if err := sink.Write(ctx, e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	// No output yet — window not completed by transition.
	if buf.Len() != 0 {
		t.Fatalf("expected no output before Close, got: %q", buf.String())
	}

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify stdout output.
	output := buf.String()
	if !strings.Contains(output, "revenue=450.00") {
		t.Errorf("expected revenue=450.00, got: %q", output)
	}
	if !strings.Contains(output, "profit=135.00") {
		t.Errorf("expected profit=135.00, got: %q", output)
	}
	if !strings.Contains(output, "events=3") {
		t.Errorf("expected events=3, got: %q", output)
	}

	// Verify file output.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	var out windowOutput
	if err := json.Unmarshal(bytes.TrimSpace(data), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.TotalRevenue != 450 {
		t.Errorf("file TotalRevenue: got %f, want 450", out.TotalRevenue)
	}
	if out.EventCount != 3 {
		t.Errorf("file EventCount: got %d, want 3", out.EventCount)
	}
}

func TestTumblingWindowSink_MultipleWindows(t *testing.T) {
	var buf bytes.Buffer
	sink, _ := newTestSink(t, 5*time.Minute, &buf)

	ctx := context.Background()
	if err := sink.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// Window 1: [10:00, 10:05)
	sink.Write(ctx, makeEvent(base, 100, 30))
	sink.Write(ctx, makeEvent(base.Add(2*time.Minute), 100, 30))

	// Window 2: [10:05, 10:10) — triggers flush of window 1
	sink.Write(ctx, makeEvent(base.Add(5*time.Minute), 200, 60))

	// Window 1 should be flushed now.
	if !strings.Contains(buf.String(), "10:00:00Z") {
		t.Errorf("expected window 1 flushed, got: %q", buf.String())
	}

	// Window 3: [10:10, 10:15) — triggers flush of window 2
	sink.Write(ctx, makeEvent(base.Add(10*time.Minute), 300, 90))

	if !strings.Contains(buf.String(), "10:05:00Z") {
		t.Errorf("expected window 2 flushed, got: %q", buf.String())
	}

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Window 3 should be flushed on Close.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 window lines, got %d:\n%s", len(lines), buf.String())
	}
}

func TestTumblingWindowSink_Aggregation(t *testing.T) {
	var buf bytes.Buffer
	sink, outPath := newTestSink(t, 5*time.Minute, &buf)

	ctx := context.Background()
	if err := sink.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// 4 events in window [10:00, 10:05).
	sink.Write(ctx, makeEvent(base, 100.50, 30.25))
	sink.Write(ctx, makeEvent(base.Add(1*time.Minute), 200.25, 60.50))
	sink.Write(ctx, makeEvent(base.Add(2*time.Minute), 150.75, 45.25))
	sink.Write(ctx, makeEvent(base.Add(3*time.Minute), 50.50, 15.00))

	// Trigger flush with next window.
	sink.Write(ctx, makeEvent(base.Add(5*time.Minute), 999, 999))

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Parse the first JSONL line from the file.
	data, _ := os.ReadFile(outPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		t.Fatal("no output lines")
	}

	var out windowOutput
	if err := json.Unmarshal([]byte(lines[0]), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantRevenue := 100.50 + 200.25 + 150.75 + 50.50
	wantProfit := 30.25 + 60.50 + 45.25 + 15.00

	if out.TotalRevenue != wantRevenue {
		t.Errorf("TotalRevenue: got %f, want %f", out.TotalRevenue, wantRevenue)
	}
	if out.TotalProfit != wantProfit {
		t.Errorf("TotalProfit: got %f, want %f", out.TotalProfit, wantProfit)
	}
	if out.EventCount != 4 {
		t.Errorf("EventCount: got %d, want 4", out.EventCount)
	}
}

func TestTumblingWindowSink_WindowBoundaries(t *testing.T) {
	var buf bytes.Buffer
	sink, outPath := newTestSink(t, 5*time.Minute, &buf)

	ctx := context.Background()
	if err := sink.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Event at exactly the boundary: 10:05:00 belongs to [10:05, 10:10).
	boundary := time.Date(2026, 1, 15, 10, 5, 0, 0, time.UTC)
	beforeBoundary := boundary.Add(-1 * time.Millisecond) // 10:04:59.999 → [10:00, 10:05)

	sink.Write(ctx, makeEvent(beforeBoundary, 100, 30))
	sink.Write(ctx, makeEvent(boundary, 200, 60))

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, _ := os.ReadFile(outPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 windows, got %d:\n%s", len(lines), string(data))
	}

	var w1, w2 windowOutput
	json.Unmarshal([]byte(lines[0]), &w1)
	json.Unmarshal([]byte(lines[1]), &w2)

	if w1.TotalRevenue != 100 {
		t.Errorf("window 1 revenue: got %f, want 100", w1.TotalRevenue)
	}
	if w2.TotalRevenue != 200 {
		t.Errorf("window 2 revenue: got %f, want 200", w2.TotalRevenue)
	}
}

func TestTumblingWindowSink_DualOutput(t *testing.T) {
	var buf bytes.Buffer
	sink, outPath := newTestSink(t, 5*time.Minute, &buf)

	ctx := context.Background()
	if err := sink.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	sink.Write(ctx, makeEvent(base, 100, 30))

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify stdout has human-readable format.
	stdout := buf.String()
	if !strings.Contains(stdout, "[2026-01-15T10:00:00Z") {
		t.Errorf("stdout missing window start: %q", stdout)
	}
	if !strings.Contains(stdout, "revenue=100.00") {
		t.Errorf("stdout missing revenue: %q", stdout)
	}

	// Verify file has JSONL format.
	data, _ := os.ReadFile(outPath)
	var out windowOutput
	if err := json.Unmarshal(bytes.TrimSpace(data), &out); err != nil {
		t.Fatalf("file output is not valid JSON: %v", err)
	}
	if out.TotalRevenue != 100 {
		t.Errorf("file revenue: got %f, want 100", out.TotalRevenue)
	}
}

func TestTumblingWindowSink_CloseFlushesRemaining(t *testing.T) {
	var buf bytes.Buffer
	sink, _ := newTestSink(t, 5*time.Minute, &buf)

	ctx := context.Background()
	if err := sink.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	// Events in two different windows, neither flushed by transition.
	sink.Write(ctx, makeEvent(base, 100, 30))
	sink.Write(ctx, makeEvent(base.Add(5*time.Minute), 200, 60))

	// Before Close, the first window was flushed by transition but the second wasn't.
	linesBefore := strings.Split(strings.TrimSpace(buf.String()), "\n")

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	linesAfter := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(linesAfter) != 2 {
		t.Fatalf("expected 2 lines after Close, got %d (before: %d):\n%s",
			len(linesAfter), len(linesBefore), buf.String())
	}
}

func TestTumblingWindowSink_EmptyInput(t *testing.T) {
	var buf bytes.Buffer
	sink, outPath := newTestSink(t, 5*time.Minute, &buf)

	ctx := context.Background()
	if err := sink.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("expected no stdout output, got: %q", buf.String())
	}

	data, _ := os.ReadFile(outPath)
	if len(bytes.TrimSpace(data)) != 0 {
		t.Errorf("expected empty output file, got: %q", string(data))
	}
}
