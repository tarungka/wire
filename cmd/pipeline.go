package main

import "github.com/tarungka/wire/sdk"

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
