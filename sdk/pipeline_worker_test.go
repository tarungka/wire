package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

type pipelineRemoteSink struct{ target *collectSink }

func (*pipelineRemoteSink) Open(context.Context) error { return nil }
func (s *pipelineRemoteSink) Write(ctx context.Context, event Event) error {
	return s.target.Write(ctx, event)
}
func (*pipelineRemoteSink) Close() error { return nil }

func TestYAMLTransformsExecuteThroughRegisteredWorkers(t *testing.T) {
	registry := NewWorkerRegistry()
	registry.RegisterPipelineTransforms()
	var sources atomic.Int32
	registry.RegisterSource("app.input", func(_ context.Context, data []byte, task WorkerTaskContext) (Source, error) {
		var config struct {
			Prefix string `json:"prefix"`
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, err
		}
		if config.Prefix != "user" {
			return nil, fmt.Errorf("connector config lost: %s", data)
		}
		sources.Add(1)
		return &sliceSource{events: []Event{{Value: []byte(fmt.Sprintf(`{"user":"shared","n":%d}`, task.Index+1)), EventTime: 1}}}, nil
	})
	sink := &collectSink{}
	registry.RegisterSink("app.output", func(context.Context, []byte, WorkerTaskContext) (Sink, error) {
		return &pipelineRemoteSink{target: sink}, nil
	})
	ctx, _, url := lifecycleCluster(t, registry)
	data := `apiVersion: wire/v1
kind: Pipeline
metadata: {name: remote-yaml}
spec:
  parallelism: 2
  state_backend: {type: hashmap}
  sources:
    - {name: input, type: application-input, config: {prefix: user}}
  transforms:
    - name: parsed
      type: json-parse
      input: input
      config: {target-field: payload}
    - name: valid
      type: filter
      input: parsed
      config: {expression: "payload.n > 0"}
    - name: expanded
      type: flat-map
      input: valid
      config: {expression: "[value, value]"}
    - name: keyed
      type: key-by
      input: expanded
      config: {key-expression: "payload.user"}
    - name: window
      type: tumbling-window
      input: keyed
      config: {size: 10ms, aggregation: count}
    - name: projected
      type: select
      input: window
      config: {fields: [key, count]}
    - name: renamed
      type: rename
      input: projected
      config: {mappings: {count: total}}
    - name: adjusted
      type: map
      input: renamed
      config: {expression: "value.total + 100"}
  sinks:
    - {name: output, type: application-output, input: adjusted}
`
	pipeline, err := ParsePipelineYAML([]byte(data), PipelineConnectors{NamedSources: map[string]string{"application-input": "app.input"}, NamedSinks: map[string]string{"application-output": "app.output"}})
	if err != nil {
		t.Fatal(err)
	}
	if sources.Load() != 0 {
		t.Fatal("submitter constructed connectors")
	}
	// Remove every compiled submitter closure. Only class names/configuration
	// may provide transform behavior beyond this process's SDK graph.
	for _, node := range pipeline.env.graph.nodes {
		node.MapFn = nil
		node.FilterFn = nil
		node.FlatMapFn = nil
		node.KeyByFn = nil
		node.Aggregator = nil
	}
	if _, err := pipeline.SetCoordinator(url).Execute(ctx); err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	if sources.Load() != 2 || len(events) != 1 || string(events[0].Value) != "104" || string(events[0].Key) != "shared" {
		t.Fatalf("sources=%d results=%v", sources.Load(), events)
	}
}

func TestPipelineWorkerRejectsMalformedDefinitions(t *testing.T) {
	valid := `{"version":1,"type":"map","config":{"expression":"value + 1"},"variables":["value"]}`
	for _, data := range []string{
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"type":"map"`, `"type":"filter"`, 1),
		strings.Replace(valid, `"expression":"value + 1"`, `"expression":"value +"`, 1),
		strings.Replace(valid, `"version":1`, `"extra":1,"version":1`, 1),
		valid + " {}", strings.Repeat(" ", maxPipelineTransformBytes+1),
	} {
		if _, err := decodePipelineTransform([]byte(data), "map"); err == nil {
			t.Fatalf("accepted malformed definition %.80s", data)
		}
	}
	node, err := decodePipelineTransform([]byte(valid), "map")
	if err != nil {
		t.Fatal(err)
	}
	event, err := node.MapFn(Event{Value: []byte("2")})
	if err != nil || string(event.Value) != "3" {
		t.Fatalf("worker expression=%s %v", event.Value, err)
	}
}

func TestYAMLNamedConnectorModeAndAmbiguity(t *testing.T) {
	data := yamlPipelineHeader + "  sinks:\n    - {name: output, type: test-sink, input: input}\n"
	bindings := PipelineConnectors{NamedSources: map[string]string{"test-source": "source"}, NamedSinks: map[string]string{"test-sink": "sink"}}
	pipeline, err := ParsePipelineYAML([]byte(data), bindings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pipeline.Execute(t.Context()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("named connectors ran locally: %v", err)
	}
	bindings.Sources = map[string]func(map[string]any) (Source, error){"test-source": func(map[string]any) (Source, error) { t.Fatal("ambiguous factory invoked"); return nil, nil }}
	if _, err = ParsePipelineYAML([]byte(data), bindings); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("ambiguity accepted: %v", err)
	}
}

func TestYAMLNamedDLQExecutesThroughRegisteredWorkers(t *testing.T) {
	registry := NewWorkerRegistry()
	registry.RegisterPipelineTransforms()
	registry.RegisterSource("app.input", func(context.Context, []byte, WorkerTaskContext) (Source, error) {
		return &sliceSource{events: []Event{{Value: []byte(`{"n":1}`)}, {Value: []byte(`not-json`)}}}, nil
	})
	output, rejected := &collectSink{}, &collectSink{}
	registry.RegisterSink("app.output", func(context.Context, []byte, WorkerTaskContext) (Sink, error) {
		return &pipelineRemoteSink{target: output}, nil
	})
	registry.RegisterSink("app.rejected", func(context.Context, []byte, WorkerTaskContext) (Sink, error) {
		return &pipelineRemoteSink{target: rejected}, nil
	})
	ctx, _, url := lifecycleCluster(t, registry)
	data := `apiVersion: wire/v1
kind: Pipeline
metadata: {name: remote-yaml-dlq}
spec:
  sources:
    - {name: input, type: application-input}
  transforms:
    - name: parsed
      type: json-parse
      input: input
      config: {target-field: payload}
      error_handling: {max_retries: 0, on_exhausted: dlq}
  sinks:
    - {name: output, type: application-output, input: parsed}
    - {name: rejected, type: application-rejected, input: __dlq__}
`
	pipeline, err := ParsePipelineYAML([]byte(data), PipelineConnectors{NamedSources: map[string]string{"application-input": "app.input"}, NamedSinks: map[string]string{"application-output": "app.output", "application-rejected": "app.rejected"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.SetCoordinator(url).Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if len(output.Events()) != 1 || len(rejected.Events()) != 1 {
		t.Fatalf("output=%v rejected=%v", output.Events(), rejected.Events())
	}
	var envelope struct {
		Original Event  `json:"original_event"`
		Operator string `json:"operator"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(rejected.Events()[0].Value, &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope.Original.Value) != "not-json" || envelope.Operator != "parsed" || envelope.Error == "" {
		t.Fatalf("DLQ lost event or attribution: %s", rejected.Events()[0].Value)
	}
}
