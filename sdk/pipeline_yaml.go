package sdk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// PipelineConnectors supplies available connector types. Factories configure
// instances but must leave I/O startup to Open. No factory runs before validation.
type PipelineConnectors struct {
	Sources map[string]func(map[string]any) (Source, error)
	Sinks   map[string]func(map[string]any) (Sink, error)
	// Instance factories receive partition identity and a private configuration
	// copy on every execution. They must return independently owned connectors.
	SourceInstances map[string]func(map[string]any, InstanceContext) (Source, error)
	SinkInstances   map[string]func(map[string]any, InstanceContext) (Sink, error)
}

type pipelineOperator struct {
	ErrorHandling *pipelineErrorPolicy `yaml:"error_handling"`
	Name          string               `yaml:"name"`
	Type          string               `yaml:"type"`
	Input         string               `yaml:"input"`
	Config        map[string]any       `yaml:"config"`
	Watermark     *rpc.WatermarkConfig `yaml:"watermark"`
}
type pipelineDocument struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		StateBackend *pipelineStateBackend `yaml:"state_backend"`
		Parallelism  int                   `yaml:"parallelism"`
		Checkpoint   struct {
			Interval time.Duration `yaml:"interval"`
			Timeout  time.Duration `yaml:"timeout"`
		} `yaml:"checkpoint"`
		Restart struct {
			Strategy    string        `yaml:"strategy"`
			MaxAttempts int           `yaml:"max-attempts"`
			Delay       time.Duration `yaml:"delay"`
		} `yaml:"restart"`
		Sources    []pipelineOperator `yaml:"sources"`
		Transforms []pipelineOperator `yaml:"transforms"`
		Sinks      []pipelineOperator `yaml:"sinks"`
	} `yaml:"spec"`
}

// YAMLPipeline is a validated definition compiled to the SDK StreamGraph.
// Execute uses the SDK graph runtime and enforces connector instance ownership.
type YAMLPipeline struct {
	Name   string
	Labels map[string]string
	env    *StreamExecutionEnvironment
}

func (p *YAMLPipeline) Graph() *StreamGraph { return p.env.graph }
func (p *YAMLPipeline) Execute(ctx context.Context) (*JobResult, error) {
	for _, node := range p.env.graph.nodes {
		if node.Parallelism > 1 && ((node.Type == NodeSource && node.SourceFactory == nil) || (node.Type == NodeSink && node.SinkFactory == nil)) {
			return nil, fmt.Errorf("%w: YAML parallel execution requires per-instance connector factories for %q", ErrInvalidConfig, node.Name)
		}
	}
	if p.env.checkpointInterval != 0 || p.env.restartStrategy.Type != RestartNone {
		for _, node := range p.env.graph.nodes {
			if (node.Type == NodeSource && node.SourceFactory == nil) || (node.Type == NodeSink && node.SinkFactory == nil) {
				return nil, fmt.Errorf("%w: YAML checkpoint/restart execution requires fresh connector instances for %q", ErrInvalidConfig, node.Name)
			}
		}
	}
	return p.env.ExecuteWithName(ctx, p.Name)
}

// ParsePipelineYAML parses one strict wire/v1 Pipeline document, validates its
// graph and expressions, and resolves connector factories into the SDK graph.
func ParsePipelineYAML(data []byte, connectors PipelineConnectors) (*YAMLPipeline, error) {
	var doc pipelineDocument
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: YAML: %v", ErrInvalidConfig, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%w: expected one YAML document", ErrInvalidConfig)
	}
	if doc.APIVersion != "wire/v1" || doc.Kind != "Pipeline" || strings.TrimSpace(doc.Metadata.Name) == "" {
		return nil, fmt.Errorf("%w: expected wire/v1 Pipeline with metadata.name", ErrInvalidConfig)
	}
	if doc.Spec.Parallelism == 0 {
		doc.Spec.Parallelism = 1
	}
	if doc.Spec.Parallelism < 1 {
		return nil, fmt.Errorf("%w: parallelism must be positive", ErrInvalidConfig)
	}
	if doc.Spec.Checkpoint.Interval < 0 || doc.Spec.Checkpoint.Timeout < 0 {
		return nil, fmt.Errorf("%w: negative checkpoint duration", ErrInvalidConfig)
	}
	if len(doc.Spec.Sources) == 0 {
		return nil, ErrNoSources
	}
	if len(doc.Spec.Sinks) == 0 {
		return nil, ErrNoSinks
	}
	env := New().SetParallelism(doc.Spec.Parallelism).SetCheckpointInterval(doc.Spec.Checkpoint.Interval)
	if doc.Spec.StateBackend != nil {
		backend, err := doc.Spec.StateBackend.compile()
		if err != nil {
			return nil, err
		}
		env.SetStateBackend(backend)
	}
	if doc.Spec.Checkpoint.Timeout > 0 {
		env.SetCheckpointTimeout(doc.Spec.Checkpoint.Timeout)
	}
	switch doc.Spec.Restart.Strategy {
	case "", "none":
		if doc.Spec.Restart.MaxAttempts != 0 || doc.Spec.Restart.Delay != 0 {
			return nil, fmt.Errorf("%w: restart options require a strategy", ErrInvalidConfig)
		}
	case "fixed-delay":
		if doc.Spec.Restart.MaxAttempts < 1 || doc.Spec.Restart.Delay <= 0 {
			return nil, fmt.Errorf("%w: invalid restart policy", ErrInvalidConfig)
		}
		env.SetRestartStrategy(FixedDelay(doc.Spec.Restart.MaxAttempts, doc.Spec.Restart.Delay))
	default:
		return nil, fmt.Errorf("%w: unsupported restart strategy", ErrInvalidConfig)
	}
	definitions := append(append(append([]pipelineOperator{}, doc.Spec.Sources...), doc.Spec.Transforms...), doc.Spec.Sinks...)
	byName := map[string]pipelineOperator{}
	kinds := map[string]StreamNodeType{}
	for i, op := range definitions {
		if op.Name == "" || op.Name == "__dlq__" || op.Type == "" {
			return nil, fmt.Errorf("%w: operator name/type required", ErrInvalidConfig)
		}
		if _, ok := byName[op.Name]; ok {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateName, op.Name)
		}
		if (i < len(doc.Spec.Sources) && connectors.Sources[op.Type] != nil && connectors.SourceInstances[op.Type] != nil) ||
			(i >= len(doc.Spec.Sources)+len(doc.Spec.Transforms) && connectors.Sinks[op.Type] != nil && connectors.SinkInstances[op.Type] != nil) {
			return nil, fmt.Errorf("%w: ambiguous connector factories for %q", ErrInvalidConfig, op.Name)
		}
		byName[op.Name] = op
		switch {
		case i < len(doc.Spec.Sources):
			kinds[op.Name] = NodeSource
			if op.Input != "" || (connectors.Sources[op.Type] == nil && connectors.SourceInstances[op.Type] == nil) {
				return nil, fmt.Errorf("%w: invalid or unavailable source %q", ErrInvalidConfig, op.Name)
			}
		case i >= len(doc.Spec.Sources)+len(doc.Spec.Transforms):
			kinds[op.Name] = NodeSink
			if connectors.Sinks[op.Type] == nil && connectors.SinkInstances[op.Type] == nil {
				return nil, fmt.Errorf("%w: unavailable sink type %q", ErrInvalidConfig, op.Type)
			}
		default:
			kinds[op.Name] = NodeMap
		}
	}
	lateOutputs := map[string]string{}
	for _, op := range definitions {
		raw, exists := op.Config["late_output"]
		if !exists {
			continue
		}
		if op.Type != "tumbling-window" && op.Type != "sliding-window" && op.Type != "session-window" {
			return nil, fmt.Errorf("%w: late_output requires a window", ErrInvalidConfig)
		}
		tag, ok := raw.(string)
		if !ok || strings.TrimSpace(tag) == "" || tag == "__dlq__" {
			return nil, fmt.Errorf("%w: invalid late output name", ErrInvalidConfig)
		}
		if _, exists := byName[tag]; exists {
			return nil, fmt.Errorf("%w: late output collides with operator %q", ErrInvalidConfig, tag)
		}
		if _, exists := lateOutputs[tag]; exists {
			return nil, fmt.Errorf("%w: duplicate late output %q", ErrInvalidConfig, tag)
		}
		lateOutputs[tag] = op.Name
	}
	inputName := func(input string) string {
		if parent, ok := lateOutputs[input]; ok {
			return parent
		}
		return input
	}
	var dlqName string
	for _, op := range definitions {
		if op.Input != "__dlq__" {
			continue
		}
		if kinds[op.Name] != NodeSink || op.ErrorHandling != nil || dlqName != "" {
			return nil, fmt.Errorf("%w: __dlq__ requires one sink without its own error policy", ErrInvalidConfig)
		}
		dlqName = op.Name
	}
	if dlqName != "" && len(doc.Spec.Sinks) == 1 {
		return nil, ErrNoSinks
	}
	var ordered []pipelineOperator
	visited := map[string]uint8{}
	var visit func(string) error
	visit = func(name string) error {
		if visited[name] == 1 {
			return fmt.Errorf("%w: %s", ErrCyclicGraph, name)
		}
		if visited[name] == 2 {
			return nil
		}
		visited[name] = 1
		op := byName[name]
		if kinds[name] != NodeSource && name != dlqName {
			if _, ok := byName[inputName(op.Input)]; !ok || kinds[inputName(op.Input)] == NodeSink {
				return fmt.Errorf("%w: %q input %q is not a source or transform", ErrInvalidConfig, name, op.Input)
			}
			if err := visit(inputName(op.Input)); err != nil {
				return err
			}
		}
		visited[name] = 2
		ordered = append(ordered, op)
		return nil
	}
	for _, op := range definitions {
		if err := visit(op.Name); err != nil {
			return nil, err
		}
	}
	variables := []string{"key", "value", "event_time", "headers", "payload"}
	for _, op := range doc.Spec.Transforms {
		if op.Type == "json-parse" {
			if target, ok := op.Config["target-field"].(string); ok {
				variables = append(variables, target)
			}
		}
	}
	expressionEnv, err := newPipelineExpressionEnv(variables)
	if err != nil {
		return nil, err
	}
	nodes := map[string]*StreamNode{}
	// Compile all transform/configuration errors before invoking any connector.
	for _, op := range ordered {
		node := &StreamNode{Name: op.Name, Type: kinds[op.Name], Parallelism: doc.Spec.Parallelism}
		if op.Watermark != nil {
			if node.Type != NodeSource {
				return nil, fmt.Errorf("%w: watermark requires a source: %q", ErrInvalidConfig, op.Name)
			}
			if op.Watermark.Strategy == "" {
				op.Watermark.Strategy = "bounded-ooo"
			}
			if err := op.Watermark.Validate(); err != nil {
				return nil, fmt.Errorf("%w: watermark for %q: %v", ErrInvalidConfig, op.Name, err)
			}
			node.Watermark = op.Watermark
		}
		if node.Type != NodeSource && node.Type != NodeSink {
			if err := compilePipelineTransform(node, op, expressionEnv); err != nil {
				return nil, fmt.Errorf("%w: transform %q: %v", ErrInvalidConfig, op.Name, err)
			}
		}
		if err := op.ErrorHandling.compile(node); err != nil {
			return nil, fmt.Errorf("%w: error handling for %q: %v", ErrInvalidConfig, op.Name, err)
		}
		if node.ErrorPolicy != nil && node.ErrorPolicy.OnExhausted == "dlq" && dlqName == "" {
			return nil, fmt.Errorf("%w: DLQ destination required for %q", ErrInvalidConfig, op.Name)
		}
		nodes[op.Name] = node
	}
	var dlq Sink
	if dlqName != "" {
		op := byName[dlqName]
		if connectors.Sinks[op.Type] == nil {
			return nil, fmt.Errorf("%w: DLQ requires a single shared sink factory", ErrInvalidConfig)
		}
		destination, factoryErr := connectors.Sinks[op.Type](op.Config)
		if factoryErr != nil || destination == nil {
			return nil, fmt.Errorf("%w: DLQ connector %q: %v", ErrInvalidConfig, dlqName, factoryErr)
		}
		if _, ok := destination.(engine.TransactionalSink); ok {
			return nil, fmt.Errorf("%w: transactional sinks cannot be used as DLQ destinations", ErrInvalidConfig)
		}
		dlq = &sharedPipelineDLQSink{sink: destination}
	}
	for _, op := range ordered {
		if op.Name == dlqName {
			continue
		}
		node := nodes[op.Name]
		if dlq != nil && node.ErrorPolicy != nil && node.ErrorPolicy.OnExhausted == "dlq" {
			node.DLQSink = dlq
		}
		switch node.Type {
		case NodeSource:
			if factory := connectors.SourceInstances[op.Type]; factory != nil {
				config := clonePipelineConfig(op.Config)
				node.SourceFactory = func(instance InstanceContext) (Source, error) { return factory(clonePipelineConfig(config), instance) }
				break
			}
			node.Source, err = connectors.Sources[op.Type](op.Config)
			if err == nil && node.Source == nil {
				err = fmt.Errorf("nil source")
			}
		case NodeSink:
			if factory := connectors.SinkInstances[op.Type]; factory != nil {
				config := clonePipelineConfig(op.Config)
				node.SinkFactory = func(instance InstanceContext) (Sink, error) { return factory(clonePipelineConfig(config), instance) }
				break
			}
			node.Sink, err = connectors.Sinks[op.Type](op.Config)
			if err == nil && node.Sink == nil {
				err = fmt.Errorf("nil sink")
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%w: connector %q: %v", ErrInvalidConfig, op.Name, err)
		}
		env.graph.addNode(node)
		if node.Type != NodeSource {
			shuffle := ShuffleForward
			if node.Type == NodeKeyBy {
				shuffle = ShuffleHash
			}
			edge := StreamEdge{SourceID: nodes[inputName(op.Input)].ID, TargetID: node.ID, Shuffle: shuffle}
			if _, ok := lateOutputs[op.Input]; ok {
				edge.SideOutput = op.Input
			}
			env.graph.edges = append(env.graph.edges, edge)
		}
	}
	if err = env.graph.validate(); err != nil {
		return nil, err
	}
	return &YAMLPipeline{Name: doc.Metadata.Name, Labels: doc.Metadata.Labels, env: env}, nil
}
