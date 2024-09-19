package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/tgk/wire/pipeline"
	"github.com/tgk/wire/sinks"
	"github.com/tgk/wire/sources"
)

func ConnectorRouter(done <-chan os.Signal) chi.Router {
	router := chi.NewRouter()

	router.Get("/{connectorName}", test())
	router.Put("/", createPipeline(done))
	router.Post("/{connectorName}", test())
	router.Delete("/{connectorName}", test())

	return router
}
func test() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Trace().Msg("Got a request")
		SendResponse(w, true, nil, "")
	}
}

func createPipeline(done <-chan os.Signal) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		var pipelineData CreatePipelineModel
		if err := json.NewDecoder(r.Body).Decode(&pipelineData); err != nil {
			log.Err(err).Msg("Error when creating a new pipeline!")
		}
		fmt.Printf("%v\n", pipelineData.Source)
		fmt.Printf("%v\n", pipelineData.Sink)

		var sourceConfig sources.SourceConfig
		var sinkConfig sinks.SinkConfig

		// Marshal the map to JSON, and then unmarshal it into the struct.
		sourceBytes, err := json.Marshal(pipelineData.Source)
		if err != nil {
			log.Err(err).Msg("Error marshalling source data")
			return
		}
		if err := json.Unmarshal(sourceBytes, &sourceConfig); err != nil {
			log.Err(err).Msg("Error un-marshalling source configuration")
			return
		}

		// Do the same for Sink
		sinkBytes, err := json.Marshal(pipelineData.Sink)
		if err != nil {
			log.Err(err).Msg("Error marshalling sink data")
			return
		}
		if err := json.Unmarshal(sinkBytes, &sinkConfig); err != nil {
			log.Err(err).Msg("Error un-marshalling sink configuration")
			return
		}

		// json.NewDecoder(pipelineData.Source)

		dataSourceInterface, err := pipeline.DataSourceFactory(sourceConfig)
		if err != nil {

		}
		dataSinkInterface, err := pipeline.DataSinkFactory(sinkConfig)
		if err != nil {

		}

		newPipeline := pipeline.NewDataPipeline(dataSourceInterface, dataSinkInterface)
		pipelineString, err := newPipeline.Show()
		if err != nil {
			log.Err(err).Send()
		}
		log.Debug().Msgf("Creating and running pipeline: %s", pipelineString)

		go newPipeline.Run(done)

		SendResponse(w, true, nil, "")
	}
}
