package sdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const parallelYAML = `apiVersion: wire/v1
kind: Pipeline
metadata: {name: parallel}
spec:
  parallelism: 3
  sources:
    - name: input
      type: partitioned
      config: {nested: {items: [original]}}
  transforms:
    - name: mapped
      type: map
      input: input
      config: {expression: "value + 10"}
  sinks:
    - name: output
      type: collected
      input: mapped
      config: {nested: {items: [original]}}
`

func TestYAMLParallelConnectorInstancesAndConfigIsolation(t *testing.T) {
	for _, runtime := range []string{"embedded", "checkpointed"} {
		t.Run(runtime, func(t *testing.T) { testYAMLParallelInstances(t, runtime == "checkpointed") })
	}
}

func testYAMLParallelInstances(t *testing.T, checkpointed bool) {
	var sourceCalls, sinkCalls atomic.Int32
	var mu sync.Mutex
	var sinks []*collectSink
	checkConfig := func(config map[string]any, instance InstanceContext) {
		t.Helper()
		if instance.Parallelism != 3 || instance.Index < 0 || instance.Index >= 3 {
			t.Errorf("instance=%+v", instance)
		}
		nested := config["nested"].(map[string]any)
		items := nested["items"].([]any)
		if items[0] != "original" {
			t.Errorf("shared configuration: %v", items)
		}
		items[0] = "mutated"
		nested["extra"] = "private"
	}
	connectors := PipelineConnectors{
		SourceInstances: map[string]func(map[string]any, InstanceContext) (Source, error){"partitioned": func(config map[string]any, instance InstanceContext) (Source, error) {
			sourceCalls.Add(1)
			checkConfig(config, instance)
			return &sliceSource{events: []Event{{Value: []byte(fmt.Sprint(instance.Index))}}}, nil
		}},
		SinkInstances: map[string]func(map[string]any, InstanceContext) (Sink, error){"collected": func(config map[string]any, instance InstanceContext) (Sink, error) {
			sinkCalls.Add(1)
			checkConfig(config, instance)
			sink := &collectSink{}
			mu.Lock()
			sinks = append(sinks, sink)
			mu.Unlock()
			return sink, nil
		}},
	}
	data := parallelYAML
	if checkpointed {
		data = strings.Replace(data, "  parallelism: 3", "  parallelism: 3\n  checkpoint: {interval: 1h}", 1)
	}
	pipeline, err := ParsePipelineYAML([]byte(data), connectors)
	if err != nil {
		t.Fatal(err)
	}
	if sourceCalls.Load() != 0 || sinkCalls.Load() != 0 {
		t.Fatal("instance factories ran during parsing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := pipeline.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if sourceCalls.Load() != 3 || sinkCalls.Load() != 3 {
		t.Fatalf("instances sources=%d sinks=%d", sourceCalls.Load(), sinkCalls.Load())
	}
	counts := map[string]int{}
	for _, sink := range sinks {
		for _, event := range sink.Events() {
			counts[string(event.Value)]++
		}
	}
	for _, value := range []string{"10", "11", "12"} {
		if counts[value] != 1 {
			t.Fatalf("records=%v", counts)
		}
	}
	if len(counts) != 3 {
		t.Fatalf("unexpected records=%v", counts)
	}
}

func TestYAMLInstanceFactoryValidationAndErrors(t *testing.T) {
	calls := 0
	failure := errors.New("factory failed")
	connectors := PipelineConnectors{
		SourceInstances: map[string]func(map[string]any, InstanceContext) (Source, error){"partitioned": func(map[string]any, InstanceContext) (Source, error) { calls++; return nil, failure }},
		SinkInstances:   map[string]func(map[string]any, InstanceContext) (Sink, error){"collected": func(map[string]any, InstanceContext) (Sink, error) { return &collectSink{}, nil }},
	}
	if _, err := ParsePipelineYAML([]byte(strings.Replace(parallelYAML, "value + 10", "value +", 1)), connectors); !errors.Is(err, ErrInvalidConfig) || calls != 0 {
		t.Fatalf("validation=%v calls=%d", err, calls)
	}
	pipeline, err := ParsePipelineYAML([]byte(parallelYAML), connectors)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pipeline.Execute(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("factory error lost: %v", err)
	}
	connectors.Sources = map[string]func(map[string]any) (Source, error){"partitioned": func(map[string]any) (Source, error) { t.Fatal("ambiguous factory invoked"); return nil, nil }}
	if _, err = ParsePipelineYAML([]byte(parallelYAML), connectors); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("ambiguous factories accepted: %v", err)
	}
}
