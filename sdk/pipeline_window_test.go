package sdk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/tarungka/wire/internal/engine"
)

func TestPipelineWindowJSONNumbersAndAtomicErrors(t *testing.T) {
	for _, tc := range []struct {
		kind       string
		aggregator Aggregator
		want       float64
	}{
		{"sum", SumAggregator{}, 3.5}, {"min", MinAggregator{}, -1.5}, {"max", MaxAggregator{}, 3},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			aggregator := pipelineWindowAggregator{Aggregator: tc.aggregator, kind: tc.kind}
			processor, err := engine.NewWindowProcessor(engine.WindowConfig{Kind: "tumbling", Size: 10, AggregationID: tc.kind}, aggregator)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"2", "-1.5", "3"} {
				if _, _, err := processor.Process(Event{Key: []byte("k"), Value: []byte(value), EventTime: 1}); err != nil {
					t.Fatal(err)
				}
			}
			for _, invalid := range []string{`{"value":100}`, `"4"`, `null`, `[4]`, `1e400`, string(make([]byte, 8))} {
				if _, _, err := processor.Process(Event{Key: []byte("k"), Value: []byte(invalid), EventTime: 1}); err == nil {
					t.Fatalf("accepted %q", invalid)
				}
			}
			results, err := processor.AdvanceWatermarkChecked(10)
			if err != nil || len(results) != 1 {
				t.Fatalf("results=%v error=%v", results, err)
			}
			events, err := aggregator.encode(context.Background(), results[0])
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(events[0].Value, &value); err != nil {
				t.Fatal(err)
			}
			if value[tc.kind] != tc.want || value["key"] != "k" || value["window_start"] != float64(0) || value["window_end"] != float64(10) {
				t.Fatalf("result=%v", value)
			}
		})
	}
}

func TestPipelineWindowOverflowDoesNotCommitPartialState(t *testing.T) {
	aggregator := pipelineWindowAggregator{Aggregator: SumAggregator{}, kind: "sum"}
	processor, err := engine.NewWindowProcessor(engine.WindowConfig{Kind: "session", Gap: 10, AggregationID: "sum"}, aggregator)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := processor.Process(Event{Value: []byte("1e308"), EventTime: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := processor.Process(Event{Value: []byte("1e308"), EventTime: 2}); err == nil {
		t.Fatal("sum overflow accepted")
	}
	result, err := processor.AdvanceWatermarkChecked(11)
	if err != nil || len(result) != 1 || math.Float64frombits(binary.BigEndian.Uint64(result[0].Value)) != 1e308 {
		t.Fatalf("failed add changed state: %v %v", result, err)
	}
	count := pipelineWindowAggregator{Aggregator: CountAggregator{}, kind: "count"}
	maxCount := binary.BigEndian.AppendUint64(nil, math.MaxUint64)
	if _, err := count.AddChecked(maxCount, Event{}); err == nil {
		t.Fatal("count overflow accepted")
	}
	if _, err := count.MergeChecked(maxCount, binary.BigEndian.AppendUint64(nil, 1)); err == nil {
		t.Fatal("merged count overflow accepted")
	}
	if _, err := count.ResultChecked([]byte{1}); err == nil {
		t.Fatal("short state accepted")
	}
}

func TestYAMLNumericWindowSelect(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want float64
	}{{"sum", 3.5}, {"min", -1.5}, {"max", 3}} {
		t.Run(tc.kind, func(t *testing.T) {
			data := yamlPipelineHeader + fmt.Sprintf(`  state_backend: {type: hashmap}
  transforms:
    - name: window
      type: tumbling-window
      input: input
      config: {size: 10ms, aggregation: %s}
    - name: projected
      type: select
      input: window
      config: {fields: [key, %s, window_start, window_end]}
  sinks:
    - {name: output, type: test-sink, input: projected}
`, tc.kind, tc.kind)
			sink := &collectSink{}
			events := []Event{{Key: []byte("k"), Value: []byte("2"), EventTime: 1}, {Key: []byte("k"), Value: []byte("-1.5"), EventTime: 2}, {Key: []byte("k"), Value: []byte("3"), EventTime: 3}}
			pipeline, err := ParsePipelineYAML([]byte(data), pipelineFactories(events, sink))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pipeline.Execute(t.Context()); err != nil {
				t.Fatal(err)
			}
			output := sink.Events()
			if len(output) != 1 {
				t.Fatalf("output=%v", output)
			}
			var value map[string]any
			if err := json.Unmarshal(output[0].Value, &value); err != nil {
				t.Fatal(err)
			}
			if value[tc.kind] != tc.want || value["key"] != "k" {
				t.Fatalf("value=%v", value)
			}
		})
	}
}

func TestPipelineWindowCountJSONPreservesUint64(t *testing.T) {
	aggregator := pipelineWindowAggregator{Aggregator: CountAggregator{}, kind: "count"}
	events, err := aggregator.encode(t.Context(), engine.WindowResult{Key: []byte("k"), Value: binary.BigEndian.AppendUint64(nil, math.MaxUint64)})
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodePipelineJSON(events[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["count"] != uint64(math.MaxUint64) {
		t.Fatalf("count lost precision: %s", events[0].Value)
	}
}
