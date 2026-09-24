package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/tarungka/wire/sdk"
	"github.com/tarungka/wire/sdk/connectors/httpapi"
)

// RegisterYAML installs JSON-configured factories separately from the legacy
// MessagePack classes. Bind YAML connector type names to class http-api.yaml.v1.
func RegisterYAML(registry *sdk.WorkerRegistry) {
	registry.RegisterSource("http-api.yaml.v1", YAMLSourceFactory())
	registry.RegisterSink("http-api.yaml.v1", YAMLSinkFactory())
}

func decodeYAMLConfig(data []byte, dst any, durations bool) error {
	if len(data) > 1<<20 {
		return fmt.Errorf("http-api: configuration exceeds 1 MiB")
	}
	if durations {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return fmt.Errorf("http-api: invalid JSON configuration")
		}
		for _, name := range []string{"timeout", "initial_delay", "max_delay"} {
			if raw, ok := fields[name]; ok {
				var value string
				if err := json.Unmarshal(raw, &value); err != nil {
					return fmt.Errorf("http-api: %s requires a duration string", name)
				}
				duration, err := time.ParseDuration(value)
				if err != nil {
					return fmt.Errorf("http-api: invalid %s duration", name)
				}
				fields[name], _ = json.Marshal(duration)
			}
		}
		var err error
		data, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("http-api: configuration must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("http-api: invalid configuration fields")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("http-api: trailing configuration data")
	}
	return nil
}

// YAMLSourceFactory accepts strict JSON configuration with snake_case fields.
func YAMLSourceFactory() sdk.WorkerSourceFactory {
	return func(_ context.Context, data []byte, _ sdk.WorkerTaskContext) (sdk.Source, error) {
		var cfg httpapi.SourceConfig
		if err := decodeYAMLConfig(data, &cfg, false); err != nil {
			return nil, err
		}
		return httpapi.NewSource(cfg)
	}
}

// YAMLSinkFactory accepts strict JSON with duration strings such as "30s".
func YAMLSinkFactory() sdk.WorkerSinkFactory {
	return func(_ context.Context, data []byte, _ sdk.WorkerTaskContext) (sdk.Sink, error) {
		var cfg httpapi.SinkConfig
		if err := decodeYAMLConfig(data, &cfg, true); err != nil {
			return nil, err
		}
		return httpapi.NewSink(cfg)
	}
}
