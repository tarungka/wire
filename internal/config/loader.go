package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultConfigPath is the config path used by pflag's default value.
// If this path is missing, Load silently skips it.
const defaultConfigPath = ".config/config.json"

// Load reads zero or more config files, merges them in order on top of
// DefaultConfig, and applies environment variable substitution.
//
// A missing file at the default path (.config/config.json) is silently
// skipped. Any other missing file returns an error.
func Load(paths []string) (WireConfig, error) {
	cfg := DefaultConfig()

	for _, p := range paths {
		overlay, err := loadFile(p)
		if err != nil {
			if os.IsNotExist(err) && p == defaultConfigPath {
				continue
			}
			return WireConfig{}, fmt.Errorf("%w: %s: %v", ErrConfigFileLoad, p, err)
		}
		mergeConfig(&cfg, &overlay)
	}

	if err := envSubstConfig(&cfg); err != nil {
		return WireConfig{}, err
	}

	return cfg, nil
}

// loadFile reads and unmarshals a single config file. The format is
// detected by file extension (.yaml/.yml → YAML, .json → JSON).
func loadFile(path string) (WireConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WireConfig{}, err
	}

	var cfg WireConfig
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return WireConfig{}, fmt.Errorf("YAML parse error: %w", err)
		}
	case ".json":
		if err := json.Unmarshal(data, &cfg); err != nil {
			return WireConfig{}, fmt.Errorf("JSON parse error: %w", err)
		}
	default:
		return WireConfig{}, fmt.Errorf("unsupported config file extension: %s", ext)
	}

	return cfg, nil
}

// mergeConfig applies non-zero values from overlay onto base.
func mergeConfig(base, overlay *WireConfig) {
	// Node
	mergeStr(&base.Node.ID, overlay.Node.ID)
	mergeStr(&base.Node.DataDir, overlay.Node.DataDir)
	mergeStr(&base.Node.StoreDB, overlay.Node.StoreDB)
	if overlay.Node.Debug {
		base.Node.Debug = true
	}

	// HTTP
	mergeStr(&base.HTTP.Addr, overlay.HTTP.Addr)
	mergeStr(&base.HTTP.AdvAddr, overlay.HTTP.AdvAddr)
	mergeStr(&base.HTTP.AllowOrigin, overlay.HTTP.AllowOrigin)
	mergeTLS(&base.HTTP.TLS, &overlay.HTTP.TLS)

	// NodeTLS
	mergeTLS(&base.NodeTLS, &overlay.NodeTLS)

	// Auth
	mergeStr(&base.Auth.File, overlay.Auth.File)

	// WriteQueue
	mergeInt(&base.WriteQueue.Capacity, overlay.WriteQueue.Capacity)
	mergeInt(&base.WriteQueue.BatchSize, overlay.WriteQueue.BatchSize)
	if overlay.WriteQueue.Timeout.Duration != 0 {
		base.WriteQueue.Timeout = overlay.WriteQueue.Timeout
	}
	if overlay.WriteQueue.Transactional {
		base.WriteQueue.Transactional = true
	}

	// Election
	mergeStr(&base.Election.Backend, overlay.Election.Backend)
	mergeStr(&base.Election.LockPath, overlay.Election.LockPath)
}

func mergeTLS(base, overlay *TLSConfig) {
	mergeStr(&base.Cert, overlay.Cert)
	mergeStr(&base.Key, overlay.Key)
	mergeStr(&base.CACert, overlay.CACert)
	mergeStr(&base.VerifyServerName, overlay.VerifyServerName)
	if overlay.VerifyClient {
		base.VerifyClient = true
	}
}

func mergeStr(base *string, overlay string) {
	if overlay != "" {
		*base = overlay
	}
}

func mergeInt(base *int, overlay int) {
	if overlay != 0 {
		*base = overlay
	}
}
