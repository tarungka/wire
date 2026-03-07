package config

import (
	"encoding/json"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDuration_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"50ms", 50 * time.Millisecond},
		{"10s", 10 * time.Second},
		{"5m", 5 * time.Minute},
		{"1h30m", 90 * time.Minute},
	}
	for _, tt := range tests {
		var d Duration
		err := yaml.Unmarshal([]byte(tt.input), &d)
		if err != nil {
			t.Fatalf("UnmarshalYAML(%q): %v", tt.input, err)
		}
		if d.Duration != tt.want {
			t.Errorf("UnmarshalYAML(%q) = %v, want %v", tt.input, d.Duration, tt.want)
		}
	}
}

func TestDuration_UnmarshalYAML_Invalid(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte("notaduration"), &d)
	if err == nil {
		t.Fatal("expected error for invalid duration, got nil")
	}
}

func TestDuration_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{`"50ms"`, 50 * time.Millisecond},
		{`"10s"`, 10 * time.Second},
		{`"5m"`, 5 * time.Minute},
	}
	for _, tt := range tests {
		var d Duration
		err := json.Unmarshal([]byte(tt.input), &d)
		if err != nil {
			t.Fatalf("UnmarshalJSON(%s): %v", tt.input, err)
		}
		if d.Duration != tt.want {
			t.Errorf("UnmarshalJSON(%s) = %v, want %v", tt.input, d.Duration, tt.want)
		}
	}
}

func TestDuration_UnmarshalJSON_Invalid(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`"notaduration"`), &d)
	if err == nil {
		t.Fatal("expected error for invalid duration, got nil")
	}
}

func TestDuration_UnmarshalJSON_NotString(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`123`), &d)
	if err == nil {
		t.Fatal("expected error for non-string JSON, got nil")
	}
}

func TestDuration_MarshalYAML(t *testing.T) {
	d := Duration{50 * time.Millisecond}
	out, err := yaml.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := "50ms\n"
	if string(out) != want {
		t.Errorf("MarshalYAML = %q, want %q", out, want)
	}
}

func TestDuration_MarshalJSON(t *testing.T) {
	d := Duration{10 * time.Second}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `"10s"`
	if string(out) != want {
		t.Errorf("MarshalJSON = %s, want %s", out, want)
	}
}
