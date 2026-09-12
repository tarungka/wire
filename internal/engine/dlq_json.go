package engine

import "encoding/json"

// MarshalDLQEvent uses the WIP-11 JSON envelope. Byte payloads use base64, so
// arbitrary binary records can be sent without losing their original contents.
func MarshalDLQEvent(e DLQEvent) ([]byte, error) {
	type record struct {
		Key       []byte            `json:"key"`
		Value     []byte            `json:"value"`
		EventTime int64             `json:"event_time"`
		Headers   map[string][]byte `json:"headers,omitempty"`
	}
	return json.Marshal(struct {
		Original   record `json:"original_event"`
		Error      string `json:"error"`
		Operator   string `json:"operator"`
		Timestamp  int64  `json:"timestamp"`
		RetryCount int    `json:"retry_count"`
	}{record{e.OriginalEvent.Key, e.OriginalEvent.Value, e.OriginalEvent.EventTime, e.OriginalEvent.Headers}, e.Error, e.OperatorName, e.Timestamp, e.RetryCount})
}
