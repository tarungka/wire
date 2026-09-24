package config

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"
)

var updateSchema = flag.Bool("update-config-schema", false, "regenerate docs/schemas/wire.schema.json")

// Generate the file format from the same field types as the loader. Semantic
// validation (including comparisons and filesystem checks) remains in Validate.
func configSchema(t reflect.Type) map[string]any {
	if t == reflect.TypeOf(Duration{}) {
		return map[string]any{"type": "string", "description": "Go duration, for example 50ms, 10s or 5m; numeric durations are rejected."}
	}
	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			properties[f.Tag.Get("koanf")] = configSchema(f.Type)
		}
		return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": configSchema(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{"type": "integer"}
	}
}

func TestConfigurationSchema(t *testing.T) {
	schema := configSchema(reflect.TypeOf(WireConfig{}))
	schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	schema["title"] = "Wire node configuration"
	schema["description"] = "Partial node configuration file; defaults and later files supply omitted fields. This schema rejects unknown keys for authoring safety; the runtime loader retains compatibility by ignoring them. After merging and environment substitution, WireConfig.Validate performs semantic and filesystem checks. Acceptance of a field does not imply runtime feature availability."
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := "../../docs/schemas/wire.schema.json"
	if *updateSchema {
		if err := os.MkdirAll("../../docs/schemas", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(existing) != string(data) {
		t.Fatal("configuration schema is stale; run go test ./internal/config -run TestConfigurationSchema -update-config-schema")
	}
}
