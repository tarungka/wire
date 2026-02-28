package operators

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeTestJSONL(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatalf("write line: %v", err)
		}
	}
	f.Close()
	return path
}

func TestJSONLSource_ReadsAllLines(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-01-15T10:00:00Z","revenue":100.0,"profit":30.0}`,
		`{"timestamp":"2026-01-15T10:01:00Z","revenue":200.0,"profit":60.0}`,
		`{"timestamp":"2026-01-15T10:02:00Z","revenue":150.0,"profit":45.0}`,
	}
	path := writeTestJSONL(t, lines)

	ctx := context.Background()
	src := NewJSONLSource(path)
	if err := src.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()

	for i := 0; i < len(lines); i++ {
		batch, err := src.ReadBatch(ctx)
		if err != nil {
			t.Fatalf("ReadBatch[%d]: %v", i, err)
		}
		if len(batch) != 1 {
			t.Fatalf("ReadBatch[%d]: got %d events, want 1", i, len(batch))
		}
	}

	// EOF
	batch, err := src.ReadBatch(ctx)
	if err != nil {
		t.Fatalf("ReadBatch[EOF]: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil batch at EOF, got %d events", len(batch))
	}
}

func TestJSONLSource_ParsesTimestamp(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-01-15T10:05:00Z","revenue":100.0,"profit":30.0}`,
	}
	path := writeTestJSONL(t, lines)

	ctx := context.Background()
	src := NewJSONLSource(path)
	if err := src.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()

	batch, err := src.ReadBatch(ctx)
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}

	want := time.Date(2026, 1, 15, 10, 5, 0, 0, time.UTC).UnixMilli()
	if batch[0].EventTime != want {
		t.Errorf("EventTime: got %d, want %d", batch[0].EventTime, want)
	}
}

func TestJSONLSource_PreservesRawJSON(t *testing.T) {
	line := `{"timestamp":"2026-01-15T10:00:00Z","revenue":150.50,"profit":42.30}`
	path := writeTestJSONL(t, []string{line})

	ctx := context.Background()
	src := NewJSONLSource(path)
	if err := src.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()

	batch, err := src.ReadBatch(ctx)
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}

	if string(batch[0].Value) != line {
		t.Errorf("Value:\n got %q\nwant %q", batch[0].Value, line)
	}
}

func TestJSONLSource_WatermarkConcurrency(t *testing.T) {
	n := 50
	lines := make([]string, n)
	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	for i := range lines {
		ts := base.Add(time.Duration(i) * time.Minute)
		lines[i] = `{"timestamp":"` + ts.Format(time.RFC3339) + `","revenue":100,"profit":30}`
	}
	path := writeTestJSONL(t, lines)

	ctx := context.Background()
	src := NewJSONLSource(path)
	if err := src.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			batch, err := src.ReadBatch(ctx)
			if err != nil {
				t.Errorf("ReadBatch error: %v", err)
				return
			}
			if batch == nil {
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n*2; i++ {
			_ = src.GenerateWatermark()
		}
	}()

	wg.Wait()
}

func TestJSONLSource_EmptyFile(t *testing.T) {
	path := writeTestJSONL(t, nil)

	ctx := context.Background()
	src := NewJSONLSource(path)
	if err := src.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()

	batch, err := src.ReadBatch(ctx)
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil batch for empty file, got %d events", len(batch))
	}
}

func TestJSONLSource_InvalidJSON(t *testing.T) {
	lines := []string{`not valid json`}
	path := writeTestJSONL(t, lines)

	ctx := context.Background()
	src := NewJSONLSource(path)
	if err := src.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()

	_, err := src.ReadBatch(ctx)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
