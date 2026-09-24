package sdk

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

// A new YAML field must be included in the checked-in authoring schema.
func TestPipelineSchemaFieldCoverage(t *testing.T) {
	data, err := os.ReadFile("../docs/schemas/pipeline.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	var check func(reflect.Type, map[string]any, string)
	check = func(typ reflect.Type, node map[string]any, path string) {
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ == reflect.TypeOf(time.Duration(0)) {
			return
		}
		if typ.Kind() == reflect.Slice {
			check(typ.Elem(), node["items"].(map[string]any), path+"[]")
			return
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		props, ok := node["properties"].(map[string]any)
		if !ok {
			t.Fatalf("missing properties for %s", path)
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := field.Tag.Get("yaml")
			// The shared operator struct accepts input and watermark fields before
			// semantic validation restricts them to transforms/sinks and sources.
			if typ == reflect.TypeOf(pipelineOperator{}) && ((name == "input" && path == "spec.sources[]") || (name == "watermark" && path != "spec.sources[]")) {
				continue
			}
			child, ok := props[name].(map[string]any)
			if !ok {
				t.Fatalf("schema missing %s.%s", path, name)
			}
			next := name
			if path != "" {
				next = path + "." + name
			}
			check(field.Type, child, next)
		}
	}
	check(reflect.TypeOf(pipelineDocument{}), schema, "")
}

func TestDocumentedPipelineExample(t *testing.T) {
	data, err := os.ReadFile("../docs/examples/pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sink := &collectSink{}
	connectors := PipelineConnectors{
		Sources: map[string]func(map[string]any) (Source, error){"app-input": func(map[string]any) (Source, error) {
			return &sliceSource{events: []Event{{Value: []byte("example")}}}, nil
		}},
		Sinks: map[string]func(map[string]any) (Sink, error){"app-output": func(map[string]any) (Sink, error) { return sink, nil }},
	}
	pipeline, err := ParsePipelineYAML(data, connectors)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	if len(events) != 1 || string(events[0].Value) != "example" {
		t.Fatalf("events=%v", events)
	}
}
