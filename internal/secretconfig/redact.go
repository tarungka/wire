package secretconfig

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

const redacted = "[REDACTED]"

// Redactor is an immutable, concurrent-safe filter for the concrete credential
// values resolved for one task. It covers literal, JSON-escaped, URL-escaped and
// base64 representations used by Wire's HTTP connectors and structured logs.
// Arbitrary application transformations require connector-side care as well.
type Redactor struct{ replacements *strings.Replacer }

func NewRedactor(values []string) *Redactor {
	patterns := make(map[string]bool)
	for _, value := range values {
		if value == "" {
			continue
		}
		encoded, _ := json.Marshal(value)
		for _, pattern := range []string{value, string(encoded[1 : len(encoded)-1]), url.QueryEscape(value), url.PathEscape(value), base64.StdEncoding.EncodeToString([]byte(value)), base64.RawStdEncoding.EncodeToString([]byte(value)), base64.URLEncoding.EncodeToString([]byte(value)), base64.RawURLEncoding.EncodeToString([]byte(value))} {
			patterns[pattern] = true
		}
	}
	keys := make([]string, 0, len(patterns))
	for pattern := range patterns {
		keys = append(keys, pattern)
	}
	// A shorter credential must not leave the suffix of a longer one exposed.
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	pairs := make([]string, 0, 2*len(keys))
	for _, key := range keys {
		pairs = append(pairs, key, redacted)
	}
	return &Redactor{replacements: strings.NewReplacer(pairs...)}
}

func (r *Redactor) String(value string) string {
	if r == nil {
		return value
	}
	return r.replacements.Replace(value)
}

// Error preserves errors.Is/errors.As so sanitizing diagnostics cannot change
// retry or checkpoint classification. Consumers must log Error(), not unwrap
// the error and serialize its underlying fields or panic stack directly.
func (r *Redactor) Error(err error) error {
	if err == nil || r == nil {
		return err
	}
	return redactedError{cause: err, message: r.String(err.Error())}
}

type redactedError struct {
	cause   error
	message string
}

func (e redactedError) Error() string { return e.message }
func (e redactedError) Unwrap() error { return e.cause }
