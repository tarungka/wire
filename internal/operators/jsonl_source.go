package operators

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

// jsonlRecord represents a single line in the JSONL input file.
type jsonlRecord struct {
	Timestamp string  `json:"timestamp"`
	Revenue   float64 `json:"revenue"`
	Profit    float64 `json:"profit"`
}

// JSONLSource reads financial records from a JSONL file, one line per ReadBatch call.
// Implements engine.SourceOperator.
type JSONLSource struct {
	path    string
	file    *os.File
	scanner *bufio.Scanner
	wm      atomic.Int64
}

// NewJSONLSource creates a source that reads from the given JSONL file path.
func NewJSONLSource(path string) *JSONLSource {
	return &JSONLSource{path: path}
}

func (s *JSONLSource) Open(ctx context.Context) error {
	f, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("jsonl source: open %q: %w", s.path, err)
	}
	s.file = f
	s.scanner = bufio.NewScanner(f)
	return nil
}

func (s *JSONLSource) Close() error {
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func (s *JSONLSource) Checkpoint(id uint64) ([]byte, error) { return nil, nil }

func (s *JSONLSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	// Lazy init: the engine does not call Open() on source operators,
	// so we open the file on first ReadBatch if needed.
	if s.scanner == nil {
		if err := s.Open(ctx); err != nil {
			return nil, err
		}
	}

	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return nil, fmt.Errorf("jsonl source: scan: %w", err)
		}
		return nil, nil // EOF
	}

	line := s.scanner.Bytes()

	var rec jsonlRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil, fmt.Errorf("jsonl source: unmarshal: %w", err)
	}

	ts, err := time.Parse(time.RFC3339, rec.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("jsonl source: parse timestamp %q: %w", rec.Timestamp, err)
	}

	eventTime := ts.UnixMilli()
	s.wm.Store(eventTime)

	// Copy line bytes so they don't alias the scanner buffer.
	value := make([]byte, len(line))
	copy(value, line)

	e := engine.Event{
		Key:       []byte(rec.Timestamp),
		Value:     value,
		EventTime: eventTime,
	}

	return []engine.Event{e}, nil
}

func (s *JSONLSource) GenerateWatermark() int64 {
	return s.wm.Load()
}
