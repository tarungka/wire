package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const maxPipelineTransformBytes = 1 << 20
const maxPipelineVariables = 1024

type pipelineTransformSpec struct {
	Version   int            `json:"version"`
	Type      string         `json:"type"`
	Config    map[string]any `json:"config"`
	Variables []string       `json:"variables"`
}

func pipelineClass(kind string) string { return "wire.yaml.v1." + kind }
func encodePipelineTransform(op pipelineOperator, variables []string) ([]byte, error) {
	if len(variables) > maxPipelineVariables {
		return nil, fmt.Errorf("%w: too many pipeline expression variables", ErrInvalidConfig)
	}
	data, err := json.Marshal(pipelineTransformSpec{Version: 1, Type: op.Type, Config: op.Config, Variables: variables})
	if err != nil {
		return nil, err
	}
	if len(data) > maxPipelineTransformBytes {
		return nil, fmt.Errorf("%w: pipeline transform exceeds 1 MiB", ErrInvalidConfig)
	}
	return data, nil
}
func decodePipelineTransform(data []byte, kind string) (*StreamNode, error) {
	if len(data) > maxPipelineTransformBytes {
		return nil, fmt.Errorf("pipeline transform exceeds 1 MiB")
	}
	var spec pipelineTransformSpec
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&spec); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("expected one transform definition")
	}
	if spec.Version != 1 || spec.Type != kind {
		return nil, fmt.Errorf("unsupported transform version or class mismatch")
	}
	if len(spec.Variables) > maxPipelineVariables {
		return nil, fmt.Errorf("too many expression variables")
	}
	env, err := newPipelineExpressionEnv(spec.Variables)
	if err != nil {
		return nil, err
	}
	node := &StreamNode{}
	if err := compilePipelineTransform(node, pipelineOperator{Type: spec.Type, Config: spec.Config}, env); err != nil {
		return nil, err
	}
	return node, nil
}

// RegisterPipelineTransforms installs versioned YAML transform classes. Call
// once before RunWorker; duplicate registration panics like other factories.
// Source/sink classes remain application registrations, not executable closures
// supplied by the submitting client. Workers compile CEL with the parser's limits.
func (r *WorkerRegistry) RegisterPipelineTransforms() {
	for _, kind := range []string{"json-parse", "map", "select", "rename", "filter", "flat-map", "key-by", "tumbling-window", "sliding-window", "session-window"} {
		name := pipelineClass(kind)
		switch kind {
		case "filter":
			r.RegisterFilter(name, func(_ context.Context, data []byte, _ WorkerTaskContext) (FilterFunc, error) {
				n, e := decodePipelineTransform(data, kind)
				if e != nil {
					return nil, e
				}
				return n.FilterFn, nil
			})
		case "flat-map":
			r.RegisterFlatMap(name, func(_ context.Context, data []byte, _ WorkerTaskContext) (FlatMapFunc, error) {
				n, e := decodePipelineTransform(data, kind)
				if e != nil {
					return nil, e
				}
				return n.FlatMapFn, nil
			})
		case "key-by":
			r.RegisterKeyBy(name, func(_ context.Context, data []byte, _ WorkerTaskContext) (KeySelector, error) {
				n, e := decodePipelineTransform(data, kind)
				if e != nil {
					return nil, e
				}
				return n.KeyByFn, nil
			})
		case "tumbling-window", "sliding-window", "session-window":
			r.RegisterWindow(name, func(_ context.Context, data []byte, _ WorkerTaskContext) (WindowDefinition, error) {
				n, e := decodePipelineTransform(data, kind)
				if e != nil {
					return WindowDefinition{}, e
				}
				return WindowDefinition{Aggregator: n.Aggregator}, nil
			})
		default:
			r.RegisterMap(name, func(_ context.Context, data []byte, _ WorkerTaskContext) (MapFunc, error) {
				n, e := decodePipelineTransform(data, kind)
				if e != nil {
					return nil, e
				}
				return n.MapFn, nil
			})
		}
	}
}

// SetCoordinator selects remote execution. All pipeline connectors must use
// NamedSources/NamedSinks and their classes must be registered on every worker.
func (p *YAMLPipeline) SetCoordinator(url string) *YAMLPipeline {
	p.env.SetMode(Cluster).SetCoordinator(url)
	return p
}
func (p *YAMLPipeline) SetCoordinatorSecurity(config CoordinatorSecurity) *YAMLPipeline {
	p.env.SetCoordinatorSecurity(config)
	return p
}
func pipelineConnectorConfig(config map[string]any) ([]byte, error) {
	if config == nil {
		config = map[string]any{}
	}
	return json.Marshal(config)
}
