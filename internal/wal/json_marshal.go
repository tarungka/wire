package wal

import "encoding/json"

// jsonMarshaler is a package-level variable that can be used for JSON operations.
// This avoids repeated calls to json.Marshal and json.Unmarshal if we were to
// use a specific implementation, but for now, it's a simple wrapper.
var jsonMarshaler = &standardJSONMarshaler{}

// standardJSONMarshaler uses the standard library's JSON capabilities.
type standardJSONMarshaler struct{}

// Marshal wraps the standard json.Marshal function.
func (j *standardJSONMarshaler) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal wraps the standard json.Unmarshal function.
func (j *standardJSONMarshaler) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
