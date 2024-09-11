package sinks

import (
	"fmt"

	"github.com/rs/zerolog/log"
)

type ElasticSink struct {
	pipelineKey            string
	pipelineName           string
	pipelineConnectionType string
	// Elasticsearch connection details
	elasticCloudId string
	elasticUrl     string
	elasticApiKey  string
	elasticIndex   string
}

func (e *ElasticSink) Init(args SinkConfig) error {
	e.pipelineKey = args.Key
	e.pipelineName = args.Name
	e.pipelineConnectionType = args.ConnectionType
	e.elasticCloudId = args.Config["cloud_id"]
	e.elasticUrl = args.Config["url"]
	e.elasticApiKey = args.Config["api_key"]
	e.elasticIndex = args.Config["index_name"]
	return nil
}

func (e *ElasticSink) Connect() error {
	log.Trace().Msg("Connecting to elaticsearch...")
	return nil
}

func (e *ElasticSink) Write(data []byte) error {
	// Write data to Elasticsearch
	log.Info().Msg("Writing to Elasticsearch")
	return nil
}

func (e *ElasticSink) Key() (string, error) {
	if e.pipelineKey == "" {
		return "", fmt.Errorf("error no pipeline key is set")
	}
	return e.pipelineKey, nil
}

func (e *ElasticSink) Name() string {
	return e.pipelineName
}

func (e *ElasticSink) Close() error {
	// Close Elasticsearch connection
	log.Info().Msg("Closing Elasticsearch connection")
	return nil
}
