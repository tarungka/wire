package sdk

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestYAMLErrorPolicyValidationBeforeFactories(t *testing.T) {
	for _, policy := range []string{
		"max_retries: -1", "backoff: mystery", "on_exhausted: ignore", "initial_delay: 1us", "initial_delay: -1ms", "initial_delay: tomorrow", "unknown_option: true", "backoff: exponential",
	} {
		t.Run(policy, func(t *testing.T) {
			called := false
			connectors := pipelineFactories(nil, &collectSink{})
			connectors.Sources["test-source"] = func(map[string]any) (Source, error) { called = true; return &sliceSource{}, nil }
			data := yamlPipelineHeader + `  sinks:
    - name: output
      type: test-sink
      input: input
      error_handling:
        ` + policy + "\n"
			if _, err := ParsePipelineYAML([]byte(data), connectors); err == nil {
				t.Fatal("accepted invalid policy")
			}
			if called {
				t.Fatal("factory called before policy validation")
			}
		})
	}
}

func TestYAMLErrorPolicyDropsMalformedJSON(t *testing.T) {
	data := yamlPipelineHeader + `  transforms:
    - name: parsed
      type: json-parse
      input: input
      config:
        target-field: payload
      error_handling:
        max_retries: 3
        backoff: exponential
        initial_delay: 100ms
        max_delay: 10s
        multiplier: 2
        on_exhausted: drop
  sinks:
    - name: output
      type: test-sink
      input: parsed
`
	sink := &collectSink{}
	pipeline, err := ParsePipelineYAML([]byte(data), pipelineFactories([]Event{{Value: []byte(`{"id":1}`)}, {Value: []byte("invalid")}}, sink))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range pipeline.Graph().nodes {
		if node.Name == "parsed" && (node.ErrorPolicy == nil || node.ErrorPolicy.InitialDelayMS != 100 || node.ErrorPolicy.MaxDelayMS != 10000) {
			t.Fatalf("policy lost: %+v", node.ErrorPolicy)
		}
	}
	if _, err = pipeline.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if events := sink.Events(); len(events) != 1 || !strings.Contains(string(events[0].Value), "1") {
		t.Fatalf("unexpected output: %+v", events)
	}
}

type yamlLifecycleSink struct {
	collectSink
	opens, closes int
}

func (s *yamlLifecycleSink) Open(context.Context) error { s.opens++; return nil }
func (s *yamlLifecycleSink) Close() error               { s.closes++; return nil }

func TestYAMLDLQSharedDestination(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	defer func() { otel.SetMeterProvider(previous); _ = provider.Shutdown(context.Background()) }()
	data := yamlPipelineHeader + `  transforms:
    - name: parsed
      type: json-parse
      input: input
      config:
        target-field: payload
      error_handling:
        on_exhausted: dlq
  sinks:
    - name: output
      type: test-sink
      input: parsed
      error_handling:
        on_exhausted: dlq
    - name: dead-letters
      type: dead-letters
      input: __dlq__
`
	main := &collectSink{}
	dlq := &yamlLifecycleSink{}
	events := make([]Event, 100)
	for i := range events {
		events[i] = Event{Key: []byte("key"), Value: []byte(`{"id":1}`)}
	}
	events[50].Value = []byte("broken json")
	factories := pipelineFactories(events, main)
	calls := 0
	factories.Sinks["dead-letters"] = func(map[string]any) (Sink, error) { calls++; return dlq, nil }
	pipeline, err := ParsePipelineYAML([]byte(data), factories)
	if err != nil {
		t.Fatal(err)
	}
	if len(pipeline.Graph().nodes) != 3 {
		t.Fatal("DLQ became a normal data node")
	}
	if _, err = pipeline.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || dlq.opens != 1 || dlq.closes != 1 {
		t.Fatalf("factory/open/close = %d/%d/%d", calls, dlq.opens, dlq.closes)
	}
	if len(main.Events()) != 99 || len(dlq.Events()) != 1 {
		t.Fatalf("main=%d dlq=%d", len(main.Events()), len(dlq.Events()))
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "wire_operator_errors_total" && metric.Name != "wire_dlq_events_total" {
				continue
			}
			for _, point := range metric.Data.(metricdata.Sum[int64]).DataPoints {
				op, _ := point.Attributes.Value("operator")
				if op.AsString() != "parsed" {
					t.Fatalf("wrong operator attribution: %+v", point)
				}
				if metric.Name == "wire_operator_errors_total" {
					class, _ := point.Attributes.Value("error_type")
					if class.AsString() != "poison" {
						t.Fatalf("wrong error class: %+v", point)
					}
				}
				counts[metric.Name] += point.Value
			}
		}
	}
	if counts["wire_operator_errors_total"] != 1 || counts["wire_dlq_events_total"] != 1 {
		t.Fatalf("missing runtime counters: %+v", counts)
	}
	var envelope struct {
		OriginalEvent struct{ Key, Value []byte } `json:"original_event"`
		Operator      string                      `json:"operator"`
	}
	if err = json.Unmarshal(dlq.Events()[0].Value, &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope.OriginalEvent.Value) != "broken json" || string(envelope.OriginalEvent.Key) != "key" || envelope.Operator != "parsed" {
		t.Fatalf("wrong envelope: %+v", envelope)
	}
}

func TestYAMLDLQRejectsInvalidBindings(t *testing.T) {
	for _, extra := range []string{
		`  sinks:
    - name: output
      type: test-sink
      input: input
      error_handling:
        on_exhausted: dlq
`,
		`  transforms:
    - name: transform
      type: json-parse
      input: __dlq__
  sinks:
    - name: output
      type: test-sink
      input: input
`,
		`  sinks:
    - name: only-dlq
      type: test-sink
      input: __dlq__
`,
		`  sinks:
    - name: output
      type: test-sink
      input: input
    - name: dlq
      type: test-sink
      input: __dlq__
      error_handling:
        on_exhausted: dlq
`,
		`  sinks:
    - name: output
      type: test-sink
      input: input
    - name: dlq1
      type: test-sink
      input: __dlq__
    - name: dlq2
      type: test-sink
      input: __dlq__
`,
		`  sinks:
    - name: __dlq__
      type: test-sink
      input: input
`,
	} {
		called := false
		factories := pipelineFactories(nil, &collectSink{})
		factories.Sources["test-source"] = func(map[string]any) (Source, error) { called = true; return &sliceSource{}, nil }
		factories.Sinks["test-sink"] = func(map[string]any) (Sink, error) { called = true; return &collectSink{}, nil }
		if _, err := ParsePipelineYAML([]byte(yamlPipelineHeader+extra), factories); err == nil {
			t.Fatalf("accepted invalid binding: %s", extra)
		}
		if called {
			t.Fatal("factory ran before binding validation")
		}
	}
}

func TestYAMLRejectsTransactionalDLQBeforeOpen(t *testing.T) {
	sink := &sdkTransactionProbe{}
	factories := pipelineFactories(nil, &collectSink{})
	factories.Sinks["txn"] = func(map[string]any) (Sink, error) { return sink, nil }
	_, err := ParsePipelineYAML([]byte(yamlPipelineHeader+`  sinks:
    - name: output
      type: test-sink
      input: input
      error_handling:
        on_exhausted: dlq
    - name: dead
      type: txn
      input: __dlq__
`), factories)
	if err == nil || sink.opened {
		t.Fatalf("err=%v opened=%t", err, sink.opened)
	}
}
