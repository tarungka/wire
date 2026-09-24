package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/tarungka/wire/internal/secretconfig"
)

func TestDLQDiagnosticRedactionPreservesOriginalRecord(t *testing.T) {
	for _, direct := range []bool{false, true} {
		channel := make(chan DLQEvent, 1)
		var captured DLQEvent
		cfg := ErrorHandlerConfig{OperatorName: "sink", OnExhausted: RouteToDLQ, SanitizeDiagnostic: secretconfig.NewRedactor([]string{"private-token"}).String}
		if direct {
			cfg.DLQWriter = func(_ context.Context, event DLQEvent) error { captured = event; return nil }
		}
		original := Event{Key: []byte("record-key"), Value: []byte("original-record")}
		err := handleExhausted(t.Context(), cfg, original, errors.New("authorization private-token rejected"), 2, channel, newTrackingErrorMetrics(), testLogger())
		if err != nil {
			t.Fatal(err)
		}
		if !direct {
			captured = <-channel
		}
		if captured.Error != "authorization [REDACTED] rejected" || captured.RetryCount != 2 || string(captured.OriginalEvent.Key) != string(original.Key) || string(captured.OriginalEvent.Value) != "original-record" {
			t.Fatalf("incorrect DLQ envelope: %+v", captured)
		}
	}
}
