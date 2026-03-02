package config

import (
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration to support YAML/JSON unmarshalling from
// human-readable strings such as "50ms", "10s", or "5m".
type Duration struct {
	time.Duration
}

// UnmarshalYAML parses a YAML scalar into a Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected scalar, got %v", value.Kind)
	}
	dur, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	d.Duration = dur
	return nil
}

// MarshalYAML returns the duration as a human-readable string.
func (d Duration) MarshalYAML() (any, error) {
	return d.Duration.String(), nil
}

// UnmarshalJSON parses a JSON string into a Duration.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dur
	return nil
}

// MarshalJSON returns the duration as a JSON string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}
