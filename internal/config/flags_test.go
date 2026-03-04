package config

import (
	"testing"

	"github.com/spf13/pflag"
)

func TestApplyFlags_ChangedOverrides(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HTTP.Addr = ":9001" // simulates config file value

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("http-listen", ":4001", "")
	fs.String("node-id", "", "")
	fs.Bool("debug", false, "")

	// Simulate: --http-listen :8080 --debug
	fs.Parse([]string{"--http-listen", ":8080", "--debug"})

	if err := ApplyFlags(&cfg, fs); err != nil {
		t.Fatal(err)
	}

	if cfg.HTTP.Addr != ":8080" {
		t.Errorf("HTTP.Addr = %q, want :8080 (CLI should override config file)", cfg.HTTP.Addr)
	}
	if !cfg.Node.Debug {
		t.Error("Node.Debug should be true after --debug")
	}
}

func TestApplyFlags_UnchangedPreservesConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HTTP.Addr = ":9001"           // config file value
	cfg.Election.Backend = "filelock" // config file value

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("http-listen", ":4001", "")
	fs.String("election-backend", "noop", "")
	fs.Bool("debug", false, "")

	// No flags passed on command line.
	fs.Parse([]string{})

	if err := ApplyFlags(&cfg, fs); err != nil {
		t.Fatal(err)
	}

	if cfg.HTTP.Addr != ":9001" {
		t.Errorf("HTTP.Addr = %q, want :9001 (unchanged flag should not override config file)", cfg.HTTP.Addr)
	}
	if cfg.Election.Backend != "filelock" {
		t.Errorf("Election.Backend = %q, want filelock", cfg.Election.Backend)
	}
}

func TestApplyFlags_AllFlags(t *testing.T) {
	cfg := DefaultConfig()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("node-id", "", "")
	fs.String("coordinator-data-dir", "data/coordinator", "")
	fs.Bool("debug", false, "")
	fs.String("http-listen", ":4001", "")
	fs.String("node-cert", "", "")
	fs.String("node-key", "", "")
	fs.String("node-ca", "", "")
	fs.Bool("node-verify-client", false, "")
	fs.String("election-backend", "noop", "")
	fs.String("election-lock-path", "data/coordinator/leader.lock", "")

	fs.Parse([]string{
		"--node-id", "node-A",
		"--coordinator-data-dir", "/data/custom",
		"--debug",
		"--http-listen", ":5555",
		"--node-cert", "/cert.pem",
		"--node-key", "/key.pem",
		"--node-ca", "/ca.pem",
		"--node-verify-client",
		"--election-backend", "filelock",
		"--election-lock-path", "/tmp/leader.lock",
	})

	if err := ApplyFlags(&cfg, fs); err != nil {
		t.Fatal(err)
	}

	if cfg.Node.ID != "node-A" {
		t.Errorf("Node.ID = %q", cfg.Node.ID)
	}
	if cfg.Node.DataDir != "/data/custom" {
		t.Errorf("Node.DataDir = %q", cfg.Node.DataDir)
	}
	if !cfg.Node.Debug {
		t.Error("Node.Debug should be true")
	}
	if cfg.HTTP.Addr != ":5555" {
		t.Errorf("HTTP.Addr = %q", cfg.HTTP.Addr)
	}
	if cfg.NodeTLS.Cert != "/cert.pem" {
		t.Errorf("NodeTLS.Cert = %q", cfg.NodeTLS.Cert)
	}
	if cfg.NodeTLS.Key != "/key.pem" {
		t.Errorf("NodeTLS.Key = %q", cfg.NodeTLS.Key)
	}
	if cfg.NodeTLS.CACert != "/ca.pem" {
		t.Errorf("NodeTLS.CACert = %q", cfg.NodeTLS.CACert)
	}
	if !cfg.NodeTLS.VerifyClient {
		t.Error("NodeTLS.VerifyClient should be true")
	}
	if cfg.Election.Backend != "filelock" {
		t.Errorf("Election.Backend = %q", cfg.Election.Backend)
	}
	if cfg.Election.LockPath != "/tmp/leader.lock" {
		t.Errorf("Election.LockPath = %q", cfg.Election.LockPath)
	}
}

func TestApplyFlags_UnmappedFlagsIgnored(t *testing.T) {
	cfg := DefaultConfig()
	orig := cfg // snapshot

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("http-listen", ":4001", "")
	fs.String("unknown-flag", "some-value", "")
	fs.Int("another-unknown", 42, "")

	// Set unmapped flags on the command line.
	fs.Parse([]string{"--unknown-flag", "boom", "--another-unknown", "99"})

	if err := ApplyFlags(&cfg, fs); err != nil {
		t.Fatal(err)
	}

	// Config should be unchanged — unmapped flags must not affect it.
	if cfg.HTTP.Addr != orig.HTTP.Addr {
		t.Errorf("HTTP.Addr = %q, want %q (unmapped flags should not change config)", cfg.HTTP.Addr, orig.HTTP.Addr)
	}
	if cfg.Node.Debug != orig.Node.Debug {
		t.Errorf("Node.Debug changed unexpectedly")
	}
}

func TestApplyFlags_NilFlagSet(t *testing.T) {
	cfg := DefaultConfig()
	if err := ApplyFlags(&cfg, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":4001" {
		t.Errorf("HTTP.Addr = %q, want :4001", cfg.HTTP.Addr)
	}
}
