package sdk

import (
	"strings"
	"testing"
	"time"
)

func TestYAMLPipelineUpdatePlan(t *testing.T) {
	original := `apiVersion: wire/v1
kind: Pipeline
metadata: {name: reload-test}
spec:
  parallelism: 1
  checkpoint: {interval: 1s, timeout: 10s}
  state_backend: {type: hashmap}
  sources:
    - {name: input, type: test-source, config: {address: first}}
  transforms:
    - {name: mapped, type: map, input: input, config: {expression: "value + 1"}}
  sinks:
    - {name: output, type: test-sink, input: mapped}
`
	bindings := PipelineConnectors{NamedSources: map[string]string{"test-source": "source"}, NamedSinks: map[string]string{"test-sink": "sink"}}
	parse := func(data string) *YAMLPipeline {
		t.Helper()
		p, err := ParsePipelineYAML([]byte(data), bindings)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := parse(original)
	for _, tc := range []struct {
		name, from, to string
		kind           PipelineUpdateKind
	}{
		{"unchanged", "value + 1", "value + 1", PipelineUnchanged},
		{"interval", "interval: 1s", "interval: 2s", PipelineIntervalUpdate},
		{"timeout", "timeout: 10s", "timeout: 20s", PipelineMigrationRequired},
		{"parallelism", "parallelism: 1", "parallelism: 2", PipelineMigrationRequired},
		{"expression", "value + 1", "value + 2", PipelineMigrationRequired},
		{"connector", "address: first", "address: second", PipelineMigrationRequired},
		{"backend", "type: hashmap", "type: pebble", PipelineMigrationRequired},
		{"name", "reload-test", "renamed-test", PipelineMigrationRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := old.PlanUpdate(parse(strings.Replace(original, tc.from, tc.to, 1)))
			if err != nil || plan.Kind != tc.kind {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			if tc.kind == PipelineIntervalUpdate && plan.CheckpointInterval != 2*time.Second {
				t.Fatal("wrong interval")
			}
		})
	}
	if old.env.checkpointInterval != time.Second {
		t.Fatal("planning mutated current pipeline")
	}
	combined := strings.Replace(strings.Replace(original, "interval: 1s", "interval: 2s", 1), "value + 1", "value + 2", 1)
	plan, err := old.PlanUpdate(parse(combined))
	if err != nil || plan.Kind != PipelineMigrationRequired {
		t.Fatalf("combined change bypassed migration: %+v %v", plan, err)
	}
}
