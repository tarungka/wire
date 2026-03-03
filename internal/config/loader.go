package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/v2"

	jsonparser "github.com/knadh/koanf/parsers/json"
	yamlparser "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
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
	ko := koanf.New(".")

	// 1. Load defaults via confmap provider.
	if err := ko.Load(confmap.Provider(defaultsMap(), "."), nil); err != nil {
		return WireConfig{}, fmt.Errorf("%w: defaults: %v", ErrConfigFileLoad, err)
	}

	// 2. Load config files in order (later files override earlier ones).
	for _, p := range paths {
		parser, err := parserForExt(p)
		if err != nil {
			return WireConfig{}, fmt.Errorf("%w: %s: %v", ErrConfigFileLoad, p, err)
		}
		if loadErr := ko.Load(file.Provider(p), parser); loadErr != nil {
			if isNotExist(loadErr) && p == defaultConfigPath {
				continue
			}
			return WireConfig{}, fmt.Errorf("%w: %s: %v", ErrConfigFileLoad, p, loadErr)
		}
	}

	// 3. Unmarshal into WireConfig.
	var cfg WireConfig
	if err := ko.UnmarshalWithConf("", &cfg, unmarshalConf()); err != nil {
		return WireConfig{}, fmt.Errorf("%w: %v", ErrConfigFileLoad, err)
	}

	// 4. Apply ${VAR:-default} substitution (koanf doesn't support this).
	if err := envSubstConfig(&cfg); err != nil {
		return WireConfig{}, err
	}
	return cfg, nil
}

// parserForExt returns the koanf parser for a file extension.
func parserForExt(path string) (koanf.Parser, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return yamlparser.Parser(), nil
	case ".json":
		return jsonparser.Parser(), nil
	default:
		return nil, fmt.Errorf("unsupported config file extension: %s", filepath.Ext(path))
	}
}

// defaultsMap converts DefaultConfig() to a map[string]any via JSON
// round-trip. This reuses the existing json struct tags for a type-safe
// conversion.
func defaultsMap() map[string]any {
	b, _ := json.Marshal(DefaultConfig())
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

// unmarshalConf returns koanf's UnmarshalConf with a custom mapstructure
// DecoderConfig that handles Duration fields.
func unmarshalConf() koanf.UnmarshalConf {
	return koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				durationDecodeHook(),
			),
			WeaklyTypedInput: true,
			TagName:          "koanf",
		},
	}
}

// durationDecodeHook returns a mapstructure DecodeHookFunc that converts
// string values (e.g. "50ms") into our Duration wrapper type.
func durationDecodeHook() mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data any) (any, error) {
		if to != reflect.TypeOf(Duration{}) {
			return data, nil
		}
		switch v := data.(type) {
		case string:
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("invalid duration %q: %w", v, err)
			}
			return Duration{d}, nil
		default:
			return data, nil
		}
	}
}

// isNotExist checks whether err (or any wrapped error) is a file-not-found
// error. koanf's file.Provider wraps the underlying os error, so we unwrap.
func isNotExist(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return os.IsNotExist(pathErr)
	}
	return os.IsNotExist(err)
}
