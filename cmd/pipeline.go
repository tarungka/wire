package main

import (
	"github.com/tarungka/wire/internal/worker"
	"github.com/tarungka/wire/sdk"
	httpworker "github.com/tarungka/wire/sdk/connectors/httpapi/worker"
)

// CLI submissions target workers that explicitly register YAML transforms and
// the public HTTP YAML factories. Parsing never starts local connector I/O.
func compileYAMLPipeline(data []byte) ([]byte, error) {
	pipeline, err := sdk.ParsePipelineYAML(data, sdk.PipelineConnectors{
		NamedSources: map[string]string{"http-api": "http-api.yaml.v1"},
		NamedSinks:   map[string]string{"http-api": "http-api.yaml.v1"},
	})
	if err != nil {
		return nil, err
	}
	return pipeline.ExportSubmission()
}

// Every worker receives a private registry. This does not mutate the legacy
// global registry and keeps connector I/O deferred until task deployment.
func pipelineWorkerRegistry() *worker.Registry {
	registry := sdk.NewWorkerRegistry()
	registry.RegisterPipelineTransforms()
	httpworker.RegisterYAML(registry)
	return registry.RuntimeRegistry()
}
