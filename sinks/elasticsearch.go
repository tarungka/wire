package sinks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/models"
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
	//
	objectContext context.Context
	esConnection  *elasticsearch.Client
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

func (e *ElasticSink) Connect(ctx context.Context) error {
	log.Trace().Msg("Connecting to elaticsearch...")
	esCfg := elasticsearch.Config{
		CloudID: e.elasticCloudId,
		APIKey:  e.elasticApiKey,
	}

	e.objectContext = ctx

	es, esErr := elasticsearch.NewClient(esCfg)
	if esErr != nil {
		return esErr
	}
	e.esConnection = es

	return nil
}

func (e *ElasticSink) Write(ctx context.Context, dataChan <-chan *models.Job, initialDataChan <-chan *models.Job) error {
	var wg sync.WaitGroup

	processChan := func(ch <-chan *models.Job) {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("Context cancelled, stopping elasticsearch write worker.")
				return
			case job, ok := <-ch:
				if !ok {
					log.Info().Msg("Channel closed, stopping elasticsearch write worker.")
					return
				}

				docData, err := job.GetData()
				if err != nil {
					log.Err(err).Msg("Error getting data from job")
					continue
				}

				docMap, ok := docData.(map[string]interface{})
				if !ok {
					log.Error().Msgf("Data is not a map[string]interface{}, but %T", docData)
					continue
				}

				var docID string
				if id, ok := docMap["_id"]; ok {
					docID = fmt.Sprintf("%v", id)
				} else {
					docID = job.ID.String()
				}

				docBytes, err := json.Marshal(docMap)
				if err != nil {
					log.Err(err).Msg("Error marshalling document to JSON")
					continue
				}

				req := esapi.IndexRequest{
					Index:      e.elasticIndex,
					DocumentID: docID,
					Body:       bytes.NewReader(docBytes),
					Refresh:    "true",
				}

				res, err := req.Do(e.objectContext, e.esConnection)
				if err != nil {
					log.Err(err).Msg("Error indexing document to Elasticsearch")
					continue
				}
				defer res.Body.Close()

				if res.IsError() {
					log.Error().Msgf("Elasticsearch indexing error: %s", res.String())
				} else {
					log.Debug().Msgf("Document indexed successfully to Elasticsearch: %v", docID)
				}
			}
		}
	}

	wg.Add(2)
	go processChan(dataChan)
	go processChan(initialDataChan)
	wg.Wait()

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

func (e *ElasticSink) Disconnect() error {
	// Close Elasticsearch connection
	log.Info().Msg("Closing Elasticsearch connection")
	return nil
}

func (e *ElasticSink) Info() string {
	return fmt.Sprintf("Key:%s|Name:%s|Type:%s", e.pipelineKey, e.pipelineName, e.pipelineConnectionType)
}
