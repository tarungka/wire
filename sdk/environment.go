package sdk

import (
	"context"
	"fmt"
	"time"
)

// StreamExecutionEnvironment is the entry point for building and executing
// streaming pipelines. It holds configuration and the logical stream graph.
type StreamExecutionEnvironment struct {
	parallelism        int
	checkpointInterval time.Duration
	checkpointTimeout  time.Duration
	restartStrategy    RestartStrategy
	mode               ExecutionMode
	graph              *StreamGraph
	executed           bool
}

// New creates a new StreamExecutionEnvironment with default settings.
func New() *StreamExecutionEnvironment {
	return &StreamExecutionEnvironment{
		parallelism:       1,
		checkpointTimeout: 10 * time.Minute,
		restartStrategy:   NoRestart(),
		mode:              Embedded,
		graph:             newStreamGraph(),
	}
}

// SetParallelism sets the default parallelism for all operators.
func (env *StreamExecutionEnvironment) SetParallelism(p int) *StreamExecutionEnvironment {
	env.parallelism = p
	return env
}

// SetCheckpointInterval enables periodic checkpointing at the given interval.
func (env *StreamExecutionEnvironment) SetCheckpointInterval(d time.Duration) *StreamExecutionEnvironment {
	env.checkpointInterval = d
	return env
}

// SetCheckpointTimeout sets the maximum time for checkpoint completion.
func (env *StreamExecutionEnvironment) SetCheckpointTimeout(d time.Duration) *StreamExecutionEnvironment {
	env.checkpointTimeout = d
	return env
}

// SetRestartStrategy configures the restart strategy for the pipeline.
func (env *StreamExecutionEnvironment) SetRestartStrategy(s RestartStrategy) *StreamExecutionEnvironment {
	env.restartStrategy = s
	return env
}

// SetMode sets the execution mode (Embedded or Cluster).
func (env *StreamExecutionEnvironment) SetMode(mode ExecutionMode) *StreamExecutionEnvironment {
	env.mode = mode
	return env
}

// AddSource adds a source to the pipeline, returning a DataStream.
func (env *StreamExecutionEnvironment) AddSource(source Source) *DataStream {
	return env.AddSourceWithName("", source)
}

// AddSourceWithName adds a named source to the pipeline.
func (env *StreamExecutionEnvironment) AddSourceWithName(name string, source Source) *DataStream {
	node := &StreamNode{
		Name:   name,
		Type:   NodeSource,
		Source: source,
	}
	id := env.graph.addNode(node)
	return &DataStream{env: env, nodeID: id}
}

// Execute validates and runs the pipeline, blocking until completion.
func (env *StreamExecutionEnvironment) Execute(ctx context.Context) (*JobResult, error) {
	return env.ExecuteWithName(ctx, "")
}

// ExecuteWithName validates and runs the pipeline with a job name.
func (env *StreamExecutionEnvironment) ExecuteWithName(ctx context.Context, jobName string) (*JobResult, error) {
	if env.executed {
		return nil, ErrAlreadyExecuted
	}
	env.executed = true

	if err := env.graph.validate(); err != nil {
		return nil, err
	}

	switch env.mode {
	case Embedded:
		executor := &embeddedExecutor{env: env}
		return executor.run(ctx, jobName)
	case Cluster:
		executor := &clusterExecutor{}
		return executor.run(ctx, jobName)
	default:
		return nil, fmt.Errorf("%w: unknown execution mode %d", ErrInvalidConfig, env.mode)
	}
}

// JobResult holds the result of a pipeline execution.
type JobResult struct {
	JobID   string
	Err     error
	Metrics JobMetrics
}

// JobMetrics holds runtime metrics for a completed pipeline.
type JobMetrics struct {
	RecordsIn  int64
	RecordsOut int64
	Duration   time.Duration
}
