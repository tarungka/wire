package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
)

// Environment overrides have a stable spelling derived from file keys:
// worker.task_slots becomes WIRE_WORKER_TASK_SLOTS. Only known configuration
// fields are considered; unrelated process variables do not create config keys.
func applyEnvironment(ko *koanf.Koanf) error {
	values := map[string]any{}
	var visit func(reflect.Type, string) error
	visit = func(typ reflect.Type, prefix string) error {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			key := field.Tag.Get("koanf")
			if prefix != "" {
				key = prefix + "." + key
			}
			if field.Type.Kind() == reflect.Struct && field.Type != reflect.TypeOf(Duration{}) {
				if err := visit(field.Type, key); err != nil {
					return err
				}
				continue
			}
			name := "WIRE_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
			value, exists := os.LookupEnv(name)
			if !exists {
				continue
			}
			if field.Type.Kind() == reflect.Slice {
				var items []string
				if err := json.Unmarshal([]byte(value), &items); err != nil || items == nil {
					return fmt.Errorf("%w: %s must be a JSON string array", ErrConfigFileLoad, name)
				}
				values[key] = items
			} else {
				values[key] = value
			}
		}
		return nil
	}
	if err := visit(reflect.TypeOf(WireConfig{}), ""); err != nil {
		return err
	}
	// WIP-18 documents this shorter alias in addition to the derived field name.
	if value, ok := os.LookupEnv("WIRE_STATE_BACKEND"); ok {
		if previous, exists := values["state.default_backend"]; exists && previous != value {
			return fmt.Errorf("%w: conflicting WIRE_STATE_BACKEND and WIRE_STATE_DEFAULT_BACKEND", ErrConfigFileLoad)
		}
		values["state.default_backend"] = value
	}
	return ko.Load(confmap.Provider(values, "."), nil)
}
