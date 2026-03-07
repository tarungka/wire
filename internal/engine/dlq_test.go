package engine

import (
	"testing"
	"time"
)

func TestDLQEvent_Fields(t *testing.T) {
	now := time.Now().UnixMilli()
	dlq := DLQEvent{
		OriginalEvent: Event{Key: []byte("k"), Value: []byte("v"), EventTime: 42},
		Error:         "bad record",
		OperatorName:  "my-map",
		Timestamp:     now,
		RetryCount:    3,
	}

	if string(dlq.OriginalEvent.Key) != "k" {
		t.Errorf("Key: got %q, want %q", dlq.OriginalEvent.Key, "k")
	}
	if string(dlq.OriginalEvent.Value) != "v" {
		t.Errorf("Value: got %q, want %q", dlq.OriginalEvent.Value, "v")
	}
	if dlq.OriginalEvent.EventTime != 42 {
		t.Errorf("EventTime: got %d, want 42", dlq.OriginalEvent.EventTime)
	}
	if dlq.Error != "bad record" {
		t.Errorf("Error: got %q, want %q", dlq.Error, "bad record")
	}
	if dlq.OperatorName != "my-map" {
		t.Errorf("OperatorName: got %q, want %q", dlq.OperatorName, "my-map")
	}
	if dlq.Timestamp != now {
		t.Errorf("Timestamp: got %d, want %d", dlq.Timestamp, now)
	}
	if dlq.RetryCount != 3 {
		t.Errorf("RetryCount: got %d, want 3", dlq.RetryCount)
	}
}
