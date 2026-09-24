package main

import (
	"os"
	"testing"

	"github.com/tarungka/wire/internal/config"
)

func TestHTTPSecurityFlagsOverrideOnlyWhenExplicit(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	for _, override := range []bool{false, true} {
		name := "preserve file settings"
		if override {
			name = "explicit CLI overrides"
		}
		t.Run(name, func(t *testing.T) {
			os.Args = []string{"wire"}
			if override {
				os.Args = append(os.Args, "--auth=cli-auth.json", "--http-cert=cli.crt", "--http-key=cli.key", "--http-ca-cert=cli-ca.crt", "--http-verify-client=false")
			}
			_, flags, err := initFlags("wire", "test", &BuildInfo{})
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.DefaultConfig()
			cfg.Auth.File = "file-auth.json"
			cfg.HTTP.TLS = config.TLSConfig{Cert: "file.crt", Key: "file.key", CACert: "file-ca.crt", VerifyClient: true}
			if err := config.ApplyFlags(&cfg, flags); err != nil {
				t.Fatal(err)
			}
			prefix := "file"
			if override {
				prefix = "cli"
			}
			wantTLS := config.TLSConfig{Cert: prefix + ".crt", Key: prefix + ".key", CACert: prefix + "-ca.crt", VerifyClient: !override}
			if cfg.Auth.File != prefix+"-auth.json" || cfg.HTTP.TLS != wantTLS {
				t.Fatalf("security flags not applied: auth=%+v tls=%+v", cfg.Auth, cfg.HTTP.TLS)
			}
		})
	}
}
