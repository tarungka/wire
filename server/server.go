package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
)

func Init(config *koanf.Koanf) {
	// log.Debug().Interface("Interface:", config.All()).Msg("Config of Konfig")
	log.Info().Msgf("Running the web server on port: %s", config.String("port"))
}

func Run(config *koanf.Koanf) {

	serverPort := config.String("port")

	router := chi.NewRouter()

	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Heartbeat("/health"))
	router.Use(middleware.CleanPath) // Not sure
	router.Use(middleware.RequestID)


	// router.Mount("/metrics")
	router.Mount("/connector", ConnectorRouter())

	log.Error().Msg(http.ListenAndServe(":" + serverPort, router).Error())
}
