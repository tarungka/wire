package sdk

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestYAMLMultipleSourcesAndKeyedFanOut(t *testing.T) {
	for _, mode := range []struct {
		name                    string
		parallelism             int
		instances, checkpointed bool
	}{
		{"legacy", 1, false, false}, {"parallel", 3, true, false}, {"checkpointed", 3, true, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			checkpoint := ""
			if mode.checkpointed {
				checkpoint = "  checkpoint: {interval: 1h}\n"
			}
			data := fmt.Sprintf(`apiVersion: wire/v1
kind: Pipeline
metadata: {name: fanout}
spec:
  parallelism: %d
%s  sources:
    - {name: first, type: partitioned, config: {base: 100}}
    - {name: second, type: partitioned, config: {base: 200}}
  transforms:
    - name: keyed
      type: key-by
      input: first
      config: {key-expression: "'shared'"}
    - name: left
      type: map
      input: keyed
      config: {expression: "value + 10"}
    - name: right
      type: map
      input: keyed
      config: {expression: "value + 20"}
  sinks:
    - {name: left-output, type: collected, input: left, config: {branch: left}}
    - {name: right-output, type: collected, input: right, config: {branch: right}}
    - {name: second-output, type: collected, input: second, config: {branch: second}}
`, mode.parallelism, checkpoint)
			var mu sync.Mutex
			destinations := map[string][]*collectSink{}
			source := func(config map[string]any, instance InstanceContext) (Source, error) {
				value := config["base"].(int) + instance.Index
				return &sliceSource{events: []Event{{Key: []byte(fmt.Sprintf("raw-%d", instance.Index)), Value: []byte(strconv.Itoa(value))}}}, nil
			}
			sink := func(config map[string]any, _ InstanceContext) (Sink, error) {
				name := config["branch"].(string)
				result := &collectSink{}
				mu.Lock()
				destinations[name] = append(destinations[name], result)
				mu.Unlock()
				return result, nil
			}
			connectors := PipelineConnectors{}
			if mode.instances {
				connectors.SourceInstances = map[string]func(map[string]any, InstanceContext) (Source, error){"partitioned": source}
				connectors.SinkInstances = map[string]func(map[string]any, InstanceContext) (Sink, error){"collected": sink}
			} else {
				connectors.Sources = map[string]func(map[string]any) (Source, error){"partitioned": func(config map[string]any) (Source, error) { return source(config, InstanceContext{Parallelism: 1}) }}
				connectors.Sinks = map[string]func(map[string]any) (Sink, error){"collected": func(config map[string]any) (Sink, error) { return sink(config, InstanceContext{Parallelism: 1}) }}
			}
			pipeline, err := ParsePipelineYAML([]byte(data), connectors)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := pipeline.Execute(ctx); err != nil {
				t.Fatal(err)
			}
			for branch, base := range map[string]int{"left": 110, "right": 120, "second": 200} {
				seen := map[string]int{}
				nonempty := 0
				for _, sink := range destinations[branch] {
					events := sink.Events()
					if len(events) > 0 {
						nonempty++
					}
					for _, event := range events {
						if branch != "second" && string(event.Key) != "shared" {
							t.Fatalf("selected key lost: %q", event.Key)
						}
						seen[string(event.Value)]++
					}
				}
				for i := 0; i < mode.parallelism; i++ {
					if seen[strconv.Itoa(base+i)] != 1 {
						t.Fatalf("branch %s records=%v", branch, seen)
					}
				}
				if len(seen) != mode.parallelism {
					t.Fatalf("extra records on %s: %v", branch, seen)
				}
				if branch != "second" && nonempty != 1 {
					t.Fatalf("selected key split over %d partitions", nonempty)
				}
			}
		})
	}
}
