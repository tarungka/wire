package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const yamlPipelineHeader = `apiVersion: wire/v1
kind: Pipeline
metadata:
  name: test-pipeline
spec:
  sources:
    - name: input
      type: test-source
`

func pipelineFactories(events []Event, sink *collectSink) PipelineConnectors {
	return PipelineConnectors{Sources: map[string]func(map[string]any) (Source, error){"test-source": func(map[string]any) (Source, error) { return &sliceSource{events: events}, nil }}, Sinks: map[string]func(map[string]any) (Sink, error){"test-sink": func(map[string]any) (Sink, error) { return sink, nil }}}
}
func TestYAMLPipelineExecution(t *testing.T) {
	data := yamlPipelineHeader + `  transforms:
    - name: parsed
      type: json-parse
      input: input
      config:
        target-field: payload
    - name: valid
      type: filter
      input: parsed
      config:
        expression: "payload.status != 'invalid'"
    - name: mapped
      type: map
      input: valid
      config:
        expression: "{'id': payload.id, 'count': payload.count + 1}"
    - name: expanded
      type: flat-map
      input: mapped
      config:
        expression: "[value, value]"
    - name: projected
      type: select
      input: expanded
      config:
        fields: [id, count]
    - name: renamed
      type: rename
      input: projected
      config:
        mappings: {count: total}
  sinks:
    - name: output
      type: test-sink
      input: renamed
`
	sink := &collectSink{}
	events := []Event{{Value: []byte(`{"status":"valid","id":9007199254740993,"count":2}`)}, {Value: []byte(`{"status":"invalid","count":3}`)}}
	pipeline, err := ParsePipelineYAML([]byte(data), pipelineFactories(events, sink))
	if err != nil {
		t.Fatal(err)
	}
	result, err := pipeline.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.JobID != "test-pipeline" || len(sink.Events()) != 2 {
		t.Fatalf("execution: %+v %d", result, len(sink.Events()))
	}
	for _, event := range sink.Events() {
		if string(event.Value) != `{"id":9007199254740993,"total":3}` {
			t.Fatalf("output: %s", event.Value)
		}
	}
}
func TestYAMLPipelineRejectsBeforeFactories(t *testing.T) {
	suffix := `  sinks:
    - name: output
      type: test-sink
      input: input
`
	cases := []string{
		strings.Replace(yamlPipelineHeader, "wire/v1", "wire/v2", 1) + suffix,
		yamlPipelineHeader + suffix + "extra: true\n",
		yamlPipelineHeader + suffix + "---\n{}\n",
		strings.Replace(yamlPipelineHeader, "test-source", "missing", 1) + suffix,
		yamlPipelineHeader + strings.Replace(suffix, "input: input", "input: nowhere", 1),
		yamlPipelineHeader + `  transforms:
    - {name: a, type: filter, input: b, config: {expression: "true"}}
    - {name: b, type: filter, input: a, config: {expression: "true"}}
` + suffix,
		yamlPipelineHeader + `  transforms:
    - {name: input, type: filter, input: input, config: {expression: "true"}}
` + suffix,
		yamlPipelineHeader + `  transforms:
    - {name: a, type: filter, input: input, config: {expression: "value +"}}
` + suffix,
		yamlPipelineHeader + `  transforms:
    - {name: a, type: filter, input: input, config: {expresson: "true"}}
` + suffix,
	}
	for i, data := range cases {
		calls := 0
		factories := pipelineFactories(nil, &collectSink{})
		factories.Sources["test-source"] = func(map[string]any) (Source, error) { calls++; return &sliceSource{}, nil }
		if _, err := ParsePipelineYAML([]byte(data), factories); err == nil {
			t.Errorf("case %d accepted invalid pipeline", i)
		}
		if calls != 0 {
			t.Errorf("case %d called connector factory before validation", i)
		}
	}
}
func TestYAMLPipelineForwardReferencesAndWindowGraph(t *testing.T) {
	data := yamlPipelineHeader + `  parallelism: 2
  checkpoint: {interval: 30s, timeout: 5m}
  restart: {strategy: fixed-delay, max-attempts: 3, delay: 10s}
  transforms:
    - name: window
      type: tumbling-window
      input: keyed
      config: {size: 1m, aggregation: count}
    - name: keyed
      type: key-by
      input: input
      config: {key-expression: "value.user_id"}
  sinks:
    - {name: output, type: test-sink, input: window}
`
	p, err := ParsePipelineYAML([]byte(data), pipelineFactories(nil, &collectSink{}))
	if err != nil {
		t.Fatal(err)
	}
	ordered := p.Graph().topoSort()
	if len(ordered) != 4 || ordered[1].Type != NodeKeyBy || ordered[2].Type != NodeWindow || ordered[2].Window.Size().String() != "1m0s" {
		t.Fatalf("graph: %+v", ordered)
	}
	if p.Graph().edges[0].Shuffle != ShuffleHash {
		t.Fatal("key-by did not produce hash edge")
	}
	key, err := ordered[1].KeyByFn(Event{Value: []byte(`{"user_id":"alice"}`)})
	if err != nil || string(key) != "alice" {
		t.Fatalf("key: %q %v", key, err)
	}
	if _, err = p.Execute(t.Context()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("unsupported window execution was not rejected: %v", err)
	}
}
func TestPipelineExpressionErrors(t *testing.T) {
	env, err := newPipelineExpressionEnv([]string{"value"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{"[1,2].map(x, x + 1)", "{'id': 18446744073709551615u}"} {
		program, err := compilePipelineExpression(env, expression)
		if err != nil {
			t.Fatal(err)
		}
		result, err := evaluatePipelineExpression(program, Event{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = json.Marshal(result); err != nil {
			t.Fatal(err)
		}
	}
	program, err := compilePipelineExpression(env, "value.missing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = evaluatePipelineExpression(program, Event{Value: []byte(`{}`)}); err == nil {
		t.Fatal("missing field evaluation succeeded")
	}
	if _, err = decodePipelineJSON([]byte(`18446744073709551616`)); err == nil {
		t.Fatal("oversized integer silently rounded")
	}
}

func TestPipelineTransformValidationAndBehavior(t *testing.T) {
	env, err := newPipelineExpressionEnv([]string{"value", "payload", "key"})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []pipelineOperator{
		{Type: "missing"}, {Type: "select", Config: map[string]any{"fields": []any{1}}},
		{Type: "rename", Config: map[string]any{"mappings": map[string]any{"a": "x", "b": "x"}}},
		{Type: "json-parse", Config: map[string]any{"target-field": "key"}},
		{Type: "tumbling-window", Config: map[string]any{"size": "0s", "aggregation": "count"}},
		{Type: "sliding-window", Config: map[string]any{"size": "1m", "slide": "bad", "aggregation": "count"}},
		{Type: "session-window", Config: map[string]any{"gap": "1m", "aggregation": "unknown"}},
	} {
		if err := compilePipelineTransform(&StreamNode{}, op, env); err == nil {
			t.Fatalf("accepted invalid transform %+v", op)
		}
	}
	for _, tc := range []struct {
		kind string
		cfg  map[string]any
	}{
		{"sliding-window", map[string]any{"size": "1m", "slide": "10s", "aggregation": "sum"}},
		{"session-window", map[string]any{"gap": "1m", "aggregation": "max"}},
	} {
		node := &StreamNode{}
		if err := compilePipelineTransform(node, pipelineOperator{Type: tc.kind, Config: tc.cfg}, env); err != nil {
			t.Fatal(err)
		}
		if node.Type != NodeWindow || node.Window == nil || node.Aggregator == nil {
			t.Fatal("window graph configuration missing")
		}
	}
	node := &StreamNode{}
	if err := compilePipelineTransform(node, pipelineOperator{Type: "rename", Config: map[string]any{"mappings": map[string]any{"a": "b", "b": "a"}}}, env); err != nil {
		t.Fatal(err)
	}
	event, err := node.MapFn(Event{Value: []byte(`{"a":1,"b":2}`)})
	if err != nil || string(event.Value) != `{"a":2,"b":1}` {
		t.Fatalf("simultaneous rename: %s %v", event.Value, err)
	}
	if _, err = node.MapFn(Event{Value: []byte(`{"a":1}`)}); err == nil {
		t.Fatal("missing rename source silently ignored")
	}
}
func TestPipelineExpressionCostBound(t *testing.T) {
	env, err := newPipelineExpressionEnv([]string{"value"})
	if err != nil {
		t.Fatal(err)
	}
	program, err := compilePipelineExpression(env, "value.map(x, value.map(y, x + y))")
	if err != nil {
		t.Fatal(err)
	}
	values := make([]int, 1000)
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = evaluatePipelineExpression(program, Event{Value: data}); err == nil {
		t.Fatal("unbounded expression evaluation")
	}
}
func TestYAMLPipelineRuntimeGuards(t *testing.T) {
	suffix := `  sinks:
    - {name: output, type: test-sink, input: input}
`
	for _, settings := range []string{"  parallelism: 2\n", "  checkpoint: {interval: 1s}\n", "  restart: {strategy: fixed-delay, max-attempts: 1, delay: 1s}\n"} {
		p, err := ParsePipelineYAML([]byte(yamlPipelineHeader+settings+suffix), pipelineFactories(nil, &collectSink{}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Execute(t.Context()); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("unsupported settings executed: %s %v", settings, err)
		}
	}
}
