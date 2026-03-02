package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_SingleYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
node:
  data_dir: /tmp/wire-data
http:
  addr: ":9001"
`), 0644)

	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Node.DataDir != "/tmp/wire-data" {
		t.Errorf("Node.DataDir = %q, want /tmp/wire-data", cfg.Node.DataDir)
	}
	if cfg.HTTP.Addr != ":9001" {
		t.Errorf("HTTP.Addr = %q, want :9001", cfg.HTTP.Addr)
	}
	// Defaults should still be set for unspecified fields.
	if cfg.WriteQueue.Capacity != 1024 {
		t.Errorf("WriteQueue.Capacity = %d, want 1024 (default)", cfg.WriteQueue.Capacity)
	}
}

func TestLoad_SingleJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{
		"node": {"data_dir": "/tmp/json-data"},
		"write_queue": {"capacity": 2048, "timeout": "100ms"}
	}`), 0644)

	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Node.DataDir != "/tmp/json-data" {
		t.Errorf("Node.DataDir = %q", cfg.Node.DataDir)
	}
	if cfg.WriteQueue.Capacity != 2048 {
		t.Errorf("WriteQueue.Capacity = %d, want 2048", cfg.WriteQueue.Capacity)
	}
	if cfg.WriteQueue.Timeout.Duration != 100*time.Millisecond {
		t.Errorf("WriteQueue.Timeout = %v, want 100ms", cfg.WriteQueue.Timeout.Duration)
	}
}

func TestLoad_TwoFileMerge(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "base.yaml")
	os.WriteFile(first, []byte(`
node:
  data_dir: /base
http:
  addr: ":8000"
`), 0644)

	second := filepath.Join(dir, "override.yaml")
	os.WriteFile(second, []byte(`
http:
  addr: ":9000"
`), 0644)

	cfg, err := Load([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	// Second file overrides HTTP.Addr.
	if cfg.HTTP.Addr != ":9000" {
		t.Errorf("HTTP.Addr = %q, want :9000", cfg.HTTP.Addr)
	}
	// First file's Node.DataDir persists.
	if cfg.Node.DataDir != "/base" {
		t.Errorf("Node.DataDir = %q, want /base", cfg.Node.DataDir)
	}
}

func TestLoad_MissingDefaultPath(t *testing.T) {
	// The default config path should be silently skipped when missing.
	cfg, err := Load([]string{defaultConfigPath})
	if err != nil {
		t.Fatalf("expected no error for missing default path, got %v", err)
	}
	// Should return defaults.
	if cfg.HTTP.Addr != ":4001" {
		t.Errorf("HTTP.Addr = %q, want :4001", cfg.HTTP.Addr)
	}
}

func TestLoad_MissingExplicitPath(t *testing.T) {
	_, err := Load([]string{"/nonexistent/config.yaml"})
	if err == nil {
		t.Fatal("expected error for missing explicit path")
	}
	if !errors.Is(err, ErrConfigFileLoad) {
		t.Errorf("expected ErrConfigFileLoad, got %v", err)
	}
}

func TestLoad_UnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`key = "value"`), 0644)

	_, err := Load([]string{path})
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !errors.Is(err, ErrConfigFileLoad) {
		t.Errorf("expected ErrConfigFileLoad, got %v", err)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte(`{{{invalid`), 0644)

	_, err := Load([]string{path})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !errors.Is(err, ErrConfigFileLoad) {
		t.Errorf("expected ErrConfigFileLoad, got %v", err)
	}
}

func TestLoad_EnvSubstitution(t *testing.T) {
	t.Setenv("WIRE_TEST_ADDR", ":7777")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
http:
  addr: "${WIRE_TEST_ADDR}"
`), 0644)

	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":7777" {
		t.Errorf("HTTP.Addr = %q, want :7777", cfg.HTTP.Addr)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	os.WriteFile(path, []byte(""), 0644)

	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	// Should return defaults.
	if cfg.HTTP.Addr != ":4001" {
		t.Errorf("HTTP.Addr = %q, want :4001", cfg.HTTP.Addr)
	}
}

func TestLoad_NoPaths(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":4001" {
		t.Errorf("HTTP.Addr = %q, want :4001", cfg.HTTP.Addr)
	}
}

func TestLoad_ExampleConfig(t *testing.T) {
	// Copy wire.yaml.example to a .yaml path so the loader recognizes it.
	data, err := os.ReadFile("../../.config/wire.yaml.example")
	if err != nil {
		t.Fatalf("failed to read example config: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "wire.yaml")
	os.WriteFile(path, data, 0644)

	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatalf("example config failed to load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("example config failed validation: %v", err)
	}
}
