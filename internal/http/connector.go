package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/pipeline"
	"github.com/tarungka/wire/sinks"
	"github.com/tarungka/wire/sources"
)

func createPipeline(w http.ResponseWriter, r *http.Request) {

	 // Read the request body
	 body, err := io.ReadAll(r.Body)
	 if err != nil {
		 log.Err(err).Msg("Error reading request body")
		 SendResponseWithHeader(w, false, nil, "error reading request body", http.StatusInternalServerError, nil)
		 return
	 }

	 // Check if the request body is empty
	 if len(body) == 0 {
		 SendResponseWithHeader(w, false, nil, "error: no request body", http.StatusBadRequest, nil)
		 return
	 }

	var pipelineData CreatePipelineModel
	if err := json.Unmarshal(body, &pipelineData); err != nil {
        log.Err(err).Msg("Error when creating a new pipeline!")
        SendResponseWithHeader(w, false, nil, "invalid request payload", http.StatusBadRequest, nil)
        return
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

	// go newPipeline.Run(wg)

	SendResponse(w, true, nil, "")
}

func deletePipeline(w http.ResponseWriter, r *http.Request) {

	key := chi.URLParam(r, "connectorName")
	kill := r.URL.Query().Get("kill")

	fmt.Printf(":-> %v %v\n", key, kill)

	dataPipeline := pipeline.GetPipelineInstance()

	dataPipeline.Info()

	closed, err := dataPipeline.Close(key)
	if err != nil {
		log.Err(err).Msgf("Error when closing data pipeline %v", key)
	}

	if !closed {
		SendResponseWithHeader(w, false, nil, "error when trying to shutdown the pipeline", http.StatusInternalServerError, nil)
		return
	}

	SendResponse(w, true, nil, err.Error())
}
