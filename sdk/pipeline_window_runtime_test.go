package sdk

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestYAMLParallelKeyedWindowRuntime(t *testing.T) {
	for _, backend := range []string{"hashmap", "pebble"} {
		for _, checkpointed := range []bool{false, true} {
			for _, window := range []struct {
				kind, config string
				outputs      int
			}{
				{"tumbling-window", "size: 10ms", 1},
				{"sliding-window", "size: 10ms, slide: 5ms", 2},
				{"session-window", "gap: 10ms", 1},
			} {
				t.Run(fmt.Sprintf("%s/checkpointed=%t/%s", backend, checkpointed, window.kind), func(t *testing.T) {
					checkpoint := ""
					if checkpointed {
						checkpoint = "  checkpoint: {interval: 1h}\n"
					}
					data := fmt.Sprintf(`apiVersion: wire/v1
kind: Pipeline
metadata: {name: keyed-window}
spec:
  parallelism: 3
  state_backend: {type: %s}
%s  sources:
    - {name: input, type: partitioned}
  transforms:
    - name: keyed
      type: key-by
      input: input
      config: {key-expression: "value.group"}
    - name: window
      type: %s
      input: keyed
      config: {%s, aggregation: count}
  sinks:
    - {name: output, type: collected, input: window}
`, backend, checkpoint, window.kind, window.config)
					var mu sync.Mutex
					var sinks []*collectSink
					p, err := ParsePipelineYAML([]byte(data), PipelineConnectors{
						SourceInstances: map[string]func(map[string]any, InstanceContext) (Source, error){"partitioned": func(_ map[string]any, instance InstanceContext) (Source, error) {
							// The original record keys differ. Only the YAML-selected key can
							// combine records from all source instances into one accumulator.
							return &sliceSource{events: []Event{{Key: []byte(fmt.Sprint(instance.Index)), Value: []byte(`{"group":"shared"}`), EventTime: 1}}}, nil
						}},
						SinkInstances: map[string]func(map[string]any, InstanceContext) (Sink, error){"collected": func(map[string]any, InstanceContext) (Sink, error) {
							sink := &collectSink{}
							mu.Lock()
							sinks = append(sinks, sink)
							mu.Unlock()
							return sink, nil
						}},
					})
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					if _, err := p.Execute(ctx); err != nil {
						t.Fatal(err)
					}
					var results []Event
					for _, sink := range sinks {
						results = append(results, sink.Events()...)
					}
					if len(results) != window.outputs {
						t.Fatalf("windows=%d want %d", len(results), window.outputs)
					}
					for _, event := range results {
						if string(event.Key) != "shared" || len(event.Value) != 8 || binary.BigEndian.Uint64(event.Value) != 3 {
							t.Fatalf("partitioned window did not combine all records: key=%q value=%x", event.Key, event.Value)
						}
					}
				})
			}
		}
	}
}
