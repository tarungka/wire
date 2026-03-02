package config

import "github.com/spf13/pflag"

// ApplyFlags overlays CLI flag values onto cfg, but only for flags that
// were explicitly set on the command line. Unchanged flags (still at their
// pflag default) do not override config-file values.
func ApplyFlags(cfg *WireConfig, flagSet *pflag.FlagSet) {
	if flagSet == nil {
		return
	}

	if flagSet.Changed("debug") {
		v, _ := flagSet.GetBool("debug")
		cfg.Node.Debug = v
	}
	if flagSet.Changed("node-id") {
		v, _ := flagSet.GetString("node-id")
		cfg.Node.ID = v
	}
	if flagSet.Changed("coordinator-data-dir") {
		v, _ := flagSet.GetString("coordinator-data-dir")
		cfg.Node.DataDir = v
	}
	if flagSet.Changed("http-listen") {
		v, _ := flagSet.GetString("http-listen")
		cfg.HTTP.Addr = v
	}
	if flagSet.Changed("node-cert") {
		v, _ := flagSet.GetString("node-cert")
		cfg.NodeTLS.Cert = v
	}
	if flagSet.Changed("node-key") {
		v, _ := flagSet.GetString("node-key")
		cfg.NodeTLS.Key = v
	}
	if flagSet.Changed("node-ca") {
		v, _ := flagSet.GetString("node-ca")
		cfg.NodeTLS.CACert = v
	}
	if flagSet.Changed("node-verify-client") {
		v, _ := flagSet.GetBool("node-verify-client")
		cfg.NodeTLS.VerifyClient = v
	}
	if flagSet.Changed("election-backend") {
		v, _ := flagSet.GetString("election-backend")
		cfg.Election.Backend = v
	}
	if flagSet.Changed("election-lock-path") {
		v, _ := flagSet.GetString("election-lock-path")
		cfg.Election.LockPath = v
	}
}
