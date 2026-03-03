package config

import (
	"encoding/json"

	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/pflag"
)

// flagToKey maps CLI flag names to koanf dotted key paths.
var flagToKey = map[string]string{
	"debug":                "node.debug",
	"node-id":              "node.id",
	"coordinator-data-dir": "node.data_dir",
	"http-listen":          "http.addr",
	"node-cert":            "node_tls.cert",
	"node-key":             "node_tls.key",
	"node-ca":              "node_tls.ca_cert",
	"node-verify-client":   "node_tls.verify_client",
	"election-backend":     "election.backend",
	"election-lock-path":   "election.lock_path",
}

// ApplyFlags overlays CLI flag values onto cfg, but only for flags that
// were explicitly set on the command line. Unchanged flags (still at their
// pflag default) do not override config-file values.
func ApplyFlags(cfg *WireConfig, flagSet *pflag.FlagSet) {
	if flagSet == nil {
		return
	}

	ko := koanf.New(".")

	// Load current cfg state so koanf knows which keys already exist.
	// This gives us Changed() semantics: unchanged flags with existing
	// values in ko are skipped by the posflag provider.
	ko.Load(confmapFromStruct(cfg), nil)

	// Load only changed flags (or flags whose keys don't exist yet).
	ko.Load(posflag.ProviderWithFlag(flagSet, ".", ko, func(f *pflag.Flag) (string, any) {
		key, ok := flagToKey[f.Name]
		if !ok {
			return "", nil // skip unmapped flags
		}
		return key, posflag.FlagVal(flagSet, f)
	}), nil)

	// Unmarshal back into cfg.
	ko.UnmarshalWithConf("", cfg, unmarshalConf())
}

// confmapFromStruct converts a WireConfig pointer into a koanf confmap
// provider using JSON round-trip, matching defaultsMap().
func confmapFromStruct(cfg *WireConfig) *confmapProvider {
	return &confmapProvider{cfg: cfg}
}

// confmapProvider implements koanf.Provider by marshalling a struct to
// map[string]any via JSON. This avoids importing confmap a second time
// and keeps the conversion logic in one place.
type confmapProvider struct {
	cfg *WireConfig
}

func (p *confmapProvider) ReadBytes() ([]byte, error) {
	return nil, nil
}

func (p *confmapProvider) Read() (map[string]any, error) {
	return structToMap(p.cfg), nil
}

func structToMap(cfg *WireConfig) map[string]any {
	b, _ := json.Marshal(cfg)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}
