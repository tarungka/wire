package config

import (
	"fmt"

	"github.com/knadh/koanf/providers/confmap"
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
func ApplyFlags(cfg *WireConfig, flagSet *pflag.FlagSet) error {
	if flagSet == nil {
		return nil
	}

	ko := koanf.New(".")

	// Load current cfg state so koanf knows which keys already exist.
	// This gives us Changed() semantics: unchanged flags with existing
	// values in ko are skipped by the posflag provider.
	cfgMap, err := structToMap(cfg)
	if err != nil {
		return fmt.Errorf("converting config to map: %w", err)
	}
	if err := ko.Load(confmap.Provider(cfgMap, "."), nil); err != nil {
		return fmt.Errorf("loading config state into koanf: %w", err)
	}

	// Load only changed flags (or flags whose keys don't exist yet).
	if err := ko.Load(posflag.ProviderWithFlag(flagSet, ".", ko, func(f *pflag.Flag) (string, any) {
		key, ok := flagToKey[f.Name]
		if !ok {
			return "", nil // skip unmapped flags
		}
		return key, posflag.FlagVal(flagSet, f)
	}), nil); err != nil {
		return fmt.Errorf("loading CLI flags into koanf: %w", err)
	}

	// Unmarshal back into cfg.
	if err := ko.UnmarshalWithConf("", cfg, unmarshalConf()); err != nil {
		return fmt.Errorf("unmarshalling flags into config: %w", err)
	}

	return nil
}
