package config

import (
	"errors"
	"testing"
)

func TestEnvSubst_Set(t *testing.T) {
	t.Setenv("WIRE_TEST_VAR", "hello")
	got, err := EnvSubst("${WIRE_TEST_VAR}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestEnvSubst_Unset(t *testing.T) {
	_, err := EnvSubst("${WIRE_UNSET_12345}")
	if err == nil {
		t.Fatal("expected error for unset var without default")
	}
	if !errors.Is(err, ErrEnvVarNotSet) {
		t.Errorf("expected ErrEnvVarNotSet, got %v", err)
	}
}

func TestEnvSubst_DefaultUsed(t *testing.T) {
	got, err := EnvSubst("${WIRE_UNSET_12345:-fallback}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestEnvSubst_DefaultNotUsed(t *testing.T) {
	t.Setenv("WIRE_TEST_VAR2", "actual")
	got, err := EnvSubst("${WIRE_TEST_VAR2:-fallback}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "actual" {
		t.Errorf("got %q, want %q", got, "actual")
	}
}

func TestEnvSubst_EmptyDefault(t *testing.T) {
	got, err := EnvSubst("${WIRE_UNSET_12345:-}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestEnvSubst_MultipleVars(t *testing.T) {
	t.Setenv("WIRE_HOST", "localhost")
	t.Setenv("WIRE_PORT", "8080")
	got, err := EnvSubst("${WIRE_HOST}:${WIRE_PORT}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "localhost:8080" {
		t.Errorf("got %q, want %q", got, "localhost:8080")
	}
}

func TestEnvSubst_NoPattern(t *testing.T) {
	got, err := EnvSubst("plain string")
	if err != nil {
		t.Fatal(err)
	}
	if got != "plain string" {
		t.Errorf("got %q, want %q", got, "plain string")
	}
}

func TestEnvSubstConfig(t *testing.T) {
	t.Setenv("WIRE_DATA_DIR", "/var/wire")
	cfg := DefaultConfig()
	cfg.Node.DataDir = "${WIRE_DATA_DIR}"
	if err := envSubstConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Node.DataDir != "/var/wire" {
		t.Errorf("Node.DataDir = %q, want %q", cfg.Node.DataDir, "/var/wire")
	}
}

func TestEnvSubstConfig_Error(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Node.DataDir = "${WIRE_MISSING_VAR_99999}"
	err := envSubstConfig(&cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrEnvVarNotSet) {
		t.Errorf("expected ErrEnvVarNotSet, got %v", err)
	}
}
