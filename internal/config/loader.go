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
// conversion. Panics on marshal/unmarshal failure since DefaultConfig()
// is a compile-time-known struct.
func defaultsMap() map[string]any {
	return mustStructToMap(DefaultConfig())
}

// mustStructToMap converts a struct to map[string]any via JSON round-trip.
// Panics on failure — use only for compile-time-known structs (e.g. DefaultConfig).
func mustStructToMap(v any) map[string]any {
	m, err := structToMap(v)
	if err != nil {
		panic(fmt.Sprintf("config: structToMap failed on known struct: %v", err))
	}
	return m
}

// structToMap converts a struct to map[string]any via JSON round-trip.
func structToMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("json.Unmarshal: %w", err)
	}
	return m, nil
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
			k := reflect.TypeOf(data).Kind()
			if k >= reflect.Int && k <= reflect.Float64 {
				return nil, fmt.Errorf("duration must be a string like \"50ms\", got number %v", v)
			}
			return data, nil
		}
	}
}

// isNotExist checks whether err (or any wrapped error) is a file-not-found
// error. errors.Is handles all standard wrapping (PathError, LinkError,
// fs.PathError, fmt.Errorf %w chains).
func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
