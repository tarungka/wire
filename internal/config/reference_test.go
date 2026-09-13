package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var updateConfigReference = flag.Bool("update-config-reference", false, "regenerate docs/configuration-reference.md")

// This test keeps the user-facing field/default/flag reference synchronized
// with the configuration structs and actual CLI override mapping.
func TestConfigurationReference(t *testing.T) {
	var b strings.Builder
	b.WriteString("# System configuration reference\n\nGenerated from the current config types, defaults, and CLI mapping. Regenerate with\n`go test ./internal/config -run TestConfigurationReference -update-config-reference`.\n\nLoad order is built-in defaults, configuration files in argument order, string\nenvironment substitution, then explicitly supplied CLI flags. Only the missing\ndefault `.config/config.json` file is ignored. Other missing files are errors.\nDuration values are strings such as `50ms`; bare numeric durations are rejected.\nLoading does not itself run semantic validation: the CLI applies overrides and\nthen calls Validate. Unknown fields are currently ignored by the loader.\n\nThis table describes accepted configuration, not runtime feature availability.\nAuthentication, TLS, and write-queue settings include fields that are not wired\ninto the current runtime; setting them does not enable those features.\n\n| Field | Type | Default | CLI override |\n| --- | --- | --- | --- |\n")
	var walk func(reflect.Value, string)
	walk = func(v reflect.Value, prefix string) {
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := typ.Field(i)
			value := v.Field(i)
			name := field.Tag.Get("koanf")
			if prefix != "" {
				name = prefix + "." + name
			}
			if value.Kind() == reflect.Struct && value.Type() != reflect.TypeOf(Duration{}) {
				walk(value, name)
				continue
			}
			kind := value.Kind().String()
			display := fmt.Sprint(value.Interface())
			if value.Type() == reflect.TypeOf(Duration{}) {
				kind = "duration string"
			}
			if display == "" {
				display = "\"\""
			}
			var flags []string
			for f, key := range flagToKey {
				if key == name {
					flags = append(flags, "`--"+f+"`")
				}
			}
			sort.Strings(flags)
			cli := strings.Join(flags, ", ")
			if cli == "" {
				cli = "—"
			}
			fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s |\n", name, kind, display, cli)
		}
	}
	walk(reflect.ValueOf(DefaultConfig()), "")
	path := filepath.Join("..", "..", "docs", "configuration-reference.md")
	if *updateConfigReference {
		if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != b.String() {
		t.Fatal("configuration reference is stale; run with -update-config-reference")
	}
}

func TestDocumentedNodeExamples(t *testing.T) {
	for _, mode := range []string{"coordinator", "worker"} {
		t.Run(mode, func(t *testing.T) {
			cfg, err := Load([]string{filepath.Join("..", "..", "docs", "examples", mode+".yaml")})
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if cfg.Mode != mode {
				t.Fatalf("mode=%q", cfg.Mode)
			}
		})
	}
}
