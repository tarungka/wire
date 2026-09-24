package main

import (
	"testing"

	"github.com/tarungka/wire/internal/config"
)

func TestRPCDoesNotSilentlyDisableRequestedTLS(t *testing.T) {
	for _, cfg := range []config.TLSConfig{{Cert: "missing-cert"}, {Key: "missing-key"}, {VerifyClient: true}} {
		if _, err := coordinatorRPCTLS(cfg); err == nil {
			t.Fatal("invalid server TLS accepted")
		}
		if _, err := workerRPCTLS(cfg); err == nil {
			t.Fatal("invalid client TLS accepted")
		}
	}
	if _, err := coordinatorRPCTLS(config.TLSConfig{CACert: "ca"}); err == nil {
		t.Fatal("CA-only server silently disabled TLS")
	}
	if cfg, err := workerRPCTLS(config.TLSConfig{}); err != nil || cfg != nil {
		t.Fatal("empty local development settings changed")
	}
}
