package http

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/pipeline"
)

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
