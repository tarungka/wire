package secretconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestLogWriterPreservesStructuredEventsWithoutCredentials(t *testing.T) {
	var output bytes.Buffer
	secret := "token\"\nwith\\escape"
	log := zerolog.New(NewRedactor([]string{secret}).LogWriter(&output)).With().Str("context", secret).Logger()
	log.Error().Err(errors.New("request rejected "+secret)).Interface("nested", map[string]any{"headers": []string{secret}}).Int64("sequence", 9007199254740993).Msg("failed: " + secret)
	var got struct {
		Context  string
		Error    string
		Message  string
		Level    string
		Sequence json.Number
		Nested   map[string][]string
	}
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Context != "[REDACTED]" || got.Error != "request rejected [REDACTED]" || got.Message != "failed: [REDACTED]" || got.Nested["headers"][0] != "[REDACTED]" {
		t.Fatal("credential retained in structured log")
	}
	if got.Level != "error" || got.Sequence.String() != "9007199254740993" {
		t.Fatal("changed log severity or numeric field")
	}
	output.Reset()
	if _, err := NewRedactor([]string{secret}).LogWriter(&output).Write([]byte("invalid " + secret)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "token") || !json.Valid(bytes.TrimSpace(output.Bytes())) {
		t.Fatal("invalid input did not fail closed")
	}
}

type shortLogWriter struct{}

func (shortLogWriter) Write([]byte) (int, error) { return 0, nil }
func TestLogWriterReportsShortWrite(t *testing.T) {
	_, err := NewRedactor(nil).LogWriter(shortLogWriter{}).Write([]byte(`{"message":"ok"}`))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("lost destination error: %v", err)
	}
}
