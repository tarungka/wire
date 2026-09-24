package secretconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rs/zerolog"
)

func TestFilteredLoggerPreservesDestinationContextAndSeverity(t *testing.T) {
	var output bytes.Buffer
	original := zerolog.New(&output).With().Str("task_id", "task").Str("credential_context", "private-token").Logger()
	filtered := NewRedactor([]string{"private-token"}).Logger(original)
	filtered.Error().Err(errors.New("rejected private-token")).Int64("sequence", 9007199254740993).Msg("task failed")
	var event struct {
		Level, Message, Error, TaskID, CredentialContext string
		Sequence                                         json.Number
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Level != "error" || event.Message != "task failed" || event.Error != "rejected [REDACTED]" || event.Sequence.String() != "9007199254740993" {
		t.Fatalf("lost structured diagnostics: %s", output.String())
	}
	if string(fields["task_id"]) != `"task"` || string(fields["credential_context"]) != `"[REDACTED]"` || bytes.Contains(output.Bytes(), []byte("private-token")) {
		t.Fatalf("lost or leaked context: %s", output.String())
	}
	output.Reset()
	original.Info().Msg("unfiltered original")
	if !bytes.Contains(output.Bytes(), []byte("private-token")) {
		t.Fatal("mutated original logger")
	}
}
