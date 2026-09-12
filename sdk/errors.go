package sdk

import (
	"errors"
	"github.com/tarungka/wire/internal/engine"
)

var (
	// ErrTransient marks retryable failures; wrap with fmt.Errorf("%w: ...", sdk.ErrTransient).
	ErrTransient = engine.ErrTransient
	// ErrFatal bypasses retry/drop/DLQ policies and fails execution.
	ErrFatal = engine.ErrFatal

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
