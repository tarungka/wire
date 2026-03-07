package sdk

import "errors"

var (
	// ErrNoSources indicates the pipeline has no source operators.
	ErrNoSources = errors.New("sdk: pipeline has no sources")

	// ErrNoSinks indicates the pipeline has no sink operators.
	ErrNoSinks = errors.New("sdk: pipeline has no sinks")

	// ErrCyclicGraph indicates the pipeline graph contains a cycle.
	ErrCyclicGraph = errors.New("sdk: pipeline graph contains a cycle")

	// ErrInvalidConfig indicates an invalid configuration value.
	ErrInvalidConfig = errors.New("sdk: invalid configuration")

	// ErrAlreadyExecuted indicates Execute was called more than once.
	ErrAlreadyExecuted = errors.New("sdk: environment already executed")

	// ErrEmptyPipeline indicates the pipeline has no operators.
	ErrEmptyPipeline = errors.New("sdk: pipeline is empty")

	// ErrDuplicateName indicates a duplicate operator name in the graph.
	ErrDuplicateName = errors.New("sdk: duplicate operator name")
)
