package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidate_DefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig should be valid, got: %v", err)
	}
}

func TestValidate_TLSCertWithoutKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NodeTLS.Cert = "/some/cert.pem"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when cert set without key")
	}
	if !errors.Is(err, ErrConfigValidation) {
		t.Errorf("expected ErrConfigValidation, got %v", err)
	}
}

func TestValidate_TLSKeyWithoutCert(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HTTP.TLS.Key = "/some/key.pem"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when key set without cert")
	}
}

func TestValidate_TLSBothSet(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, []byte("cert"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig()
	cfg.NodeTLS.Cert = certPath
	cfg.NodeTLS.Key = keyPath
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid TLS pair should pass, got: %v", err)
	}
}

func TestValidate_WriteQueueBatchExceedsCapacity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WriteQueue.Capacity = 100
	cfg.WriteQueue.BatchSize = 200
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when batch_size > capacity")
	}
	if !errors.Is(err, ErrConfigValidation) {
		t.Errorf("expected ErrConfigValidation, got %v", err)
	}
}

func TestValidate_NegativeCapacity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WriteQueue.Capacity = -1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative capacity")
	}
}

func TestValidate_NegativeTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WriteQueue.Timeout = Duration{-1 * time.Millisecond}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

func TestValidate_UnknownElectionBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Election.Backend = "raft"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for unknown election backend")
	}
}

func TestValidate_FileNotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.File = "/nonexistent/auth.json"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing auth file")
	}
}

func TestValidate_ObservabilityEnabledRequiresAddr(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Observability.Enabled = true
	cfg.Observability.MetricsAddr = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when observability is enabled with empty metrics_addr")
	}
}

func TestValidate_ObservabilityDisabledAllowsEmptyAddr(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Observability.Enabled = false
	cfg.Observability.MetricsAddr = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled observability with empty addr should validate: %v", err)
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NodeTLS.Cert = "/missing/cert.pem" // cert without key
	cfg.Election.Backend = "invalid"
	cfg.WriteQueue.Capacity = -1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected multiple validation errors")
	}
	if !errors.Is(err, ErrConfigValidation) {
		t.Errorf("expected ErrConfigValidation, got %v", err)
	}
	// The error string should mention multiple issues.
	msg := err.Error()
	if len(msg) < 30 {
		t.Errorf("expected detailed error message, got: %s", msg)
	}
}
