package sdk

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tarungka/wire/internal/engine"
)

const yamlBackendWindow = `  transforms:
    - name: window
      type: tumbling-window
      input: input
      config: {size: 1m, aggregation: count}
  sinks:
    - {name: output, type: test-sink, input: window}
`

func TestPipelineStateBackendValidation(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		want         int
		valid        bool
	}{
		{"default", "{type: hashmap}", 256, true},
		{"unlimited", "{type: hashmap, hashmap: {max_memory_mb: 0}}", 0, true},
		{"bounded", "{type: hashmap, hashmap: {max_memory_mb: 1}}", 1, true},
		{"negative", "{type: hashmap, hashmap: {max_memory_mb: -1}}", 0, false},
		{"overflow", "{type: hashmap, hashmap: {max_memory_mb: 8796093022208}}", 0, false},
		{"unknown", "{type: redis}", 0, false},
		{"typo", "{type: hashmap, hashmap: {max_memory: 1}}", 0, false},
		{"compaction", "{type: pebble, pebble: {max_compaction_concurrency: -1}}", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			factories := pipelineFactories(nil, &collectSink{})
			factories.Sources["test-source"] = func(map[string]any) (Source, error) { calls++; return &sliceSource{}, nil }
			p, err := ParsePipelineYAML([]byte(yamlPipelineHeader+"  state_backend: "+tc.config+"\n"+yamlBackendWindow), factories)
			if !tc.valid {
				if !errors.Is(err, ErrInvalidConfig) || calls != 0 {
					t.Fatalf("error=%v factories=%d", err, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !p.env.stateBackendSet || p.env.stateBackend.MaxMemoryMB != tc.want {
				t.Fatalf("backend=%+v", p.env.stateBackend)
			}
		})
	}
}

func TestYAMLWindowUsesSelectedBackendAndSDKOverride(t *testing.T) {
	data := []byte(yamlPipelineHeader + "  state_backend: {type: hashmap, hashmap: {max_memory_mb: 1}}\n" + yamlBackendWindow)
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "yaml_limit", true: "sdk_override"}[override], func(t *testing.T) {
			p, err := ParsePipelineYAML(data, pipelineFactories([]Event{{Key: bytes.Repeat([]byte("k"), 2*1024*1024), Value: []byte("1"), EventTime: 1}}, &collectSink{}))
			if err != nil {
				t.Fatal(err)
			}
			if override {
				p.SetStateBackend(NewHashMapStateBackend(16))
			}
			_, err = p.Execute(context.Background())
			if override {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, engine.ErrMemoryLimitExceeded) {
				t.Fatalf("expected configured memory limit, got %v", err)
			}
		})
	}
	p, err := ParsePipelineYAML([]byte(strings.Replace(string(data), "type: hashmap, hashmap: {max_memory_mb: 1}", "type: pebble", 1)), pipelineFactories(nil, &collectSink{}))
	if err != nil {
		t.Fatal(err)
	}
	p.SetStateBackend(StateBackendConfig{Type: "unknown"})
	if _, err = p.Execute(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid SDK override accepted: %v", err)
	}
}
