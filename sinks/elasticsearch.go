package sinks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
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

	// e.objectContext = context.Background()
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

// Accepts a byte array of json data and writes to elastic search index
func (e *ElasticSink) Write(done <-chan interface{},mongoChan <-chan []byte) error {
	// Receive data from the MongoSource channel
	for changeDocBytes := range mongoChan {

		var changeDoc map[string]interface{}
		changeDocBytes = []byte(`{"doc":` + string(changeDocBytes) + `}`)
		// Convert change document to JSON for Elasticsearch
		err := json.Unmarshal(changeDocBytes, &changeDoc)
		if err != nil {
			log.Err(err).Msg("Error un-marshalling MongoDB change document")
			continue
		}

		log.Trace().Msgf("Writing data to elastic index: %s", changeDoc)

		// TODO: Doing this for the initial dev, will optimize this in the later versions
		// Convert change document back to JSON for Elasticsearch (optional: already bytes in mongoChan)
		// However, this is technically not required as we're already reading from a byte channel.
		// So you could directly send changeDocBytes if Mongo source is already sending JSON-encoded bytes.
		// But for clarity, we're re-marshalling it here.
		data, err := json.Marshal(changeDoc)
		if err != nil {
			log.Err(err).Msg("Error marshalling MongoDB change document to JSON:")
			continue
		}

		fullDocument, ok := changeDoc["doc"]
		if !ok {
			log.Err(fmt.Errorf("no doc in the document")).Msg("No 'doc' in the document!")
			continue
		}
		documentID, ok := fullDocument.(map[string]interface{})["_id"]
		if !ok {
			log.Err(fmt.Errorf("missing _id field")).Msg("Change document is missing _id field")
			continue
		}

		log.Debug().Msgf("Event document ID: %s", documentID)
		log.Trace().Msgf("Writing data to elastic index: %s", data)

		// Create an Elasticsearch index request
		req := esapi.IndexRequest{
			Index:      e.elasticIndex,                // Elasticsearch index name
			DocumentID: fmt.Sprintf("%v", documentID), // Assuming the changeDoc has an "_id" field
			Body:       bytes.NewReader(data),
			Refresh:    "true", // Auto-refresh to make the document available immediately
		}

		// Execute the request
		res, err := req.Do(e.objectContext, e.esConnection)
		if err != nil {
			log.Err(err).Msg("Error indexing document to Elasticsearch:")
			continue
		}
		defer res.Body.Close()

		if res.IsError() {
			log.Printf("Elasticsearch indexing error: %s", res.String())
			return fmt.Errorf("%s", res.String())
		} else {
			log.Printf("Document indexed successfully to Elasticsearch: %v", documentID)
		}
	}

	log.Trace().Msg("Closed the upstream channel, need to clean up the es connection also")

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
