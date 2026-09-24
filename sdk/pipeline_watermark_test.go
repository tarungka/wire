package sdk

import (
	"strings"
	"testing"
	"time"
)

func TestYAMLWatermarkConfiguration(t *testing.T) {
	const sink = "  sinks:\n    - name: output\n      type: test-sink\n      input: input\n"
	data := yamlPipelineHeader + "      watermark:\n        strategy: bounded-ooo\n        max_ooo: 5s\n        emit_interval: 20ms\n        idle_timeout: 2s\n" + sink
	pipeline, err := ParsePipelineYAML([]byte(data), pipelineFactories(nil, &collectSink{}))
	if err != nil {
		t.Fatal(err)
	}
	cfg := pipeline.Graph().toJobGraph(1).Operators[0].Watermark
	if cfg == nil || cfg.MaxOOO == nil || *cfg.MaxOOO != 5*time.Second || cfg.EmitInterval != 20*time.Millisecond || cfg.IdleTimeout != 2*time.Second {
		t.Fatalf("lost YAML watermark configuration: %+v", cfg)
	}
	for _, invalid := range []string{
		strings.Replace(data, "strategy: bounded-ooo", "strategy: bad", 1),
		strings.Replace(data, "max_ooo: 5s", "max_ooo: -1s", 1),
		strings.Replace(data, "emit_interval: 20ms", "emit_interval: nonsense", 1),
		strings.Replace(data, "idle_timeout: 2s", "unknown_timeout: 2s", 1),
		yamlPipelineHeader + sink + "      watermark: {strategy: monotonic}\n",
	} {
		factories := pipelineFactories(nil, &collectSink{})
		factories.Sources["test-source"] = func(map[string]any) (Source, error) {
			t.Fatal("factory ran before watermark validation")
			return nil, nil
		}
		if _, err := ParsePipelineYAML([]byte(invalid), factories); err == nil {
			t.Fatalf("invalid watermark accepted: %s", invalid)
		}
	}
}
