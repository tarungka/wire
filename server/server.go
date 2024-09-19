package server

import (
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
)

func Init(config *koanf.Koanf) {
	// log.Debug().Interface("Interface:", config.All()).Msg("Config of Konfig")
	log.Info().Msgf("Running the web server on port: %s", config.String("port"))
}

func Run(done <-chan os.Signal, config *koanf.Koanf) {
	serverPort := config.String("port")

	router := chi.NewRouter()

	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Heartbeat("/health"))
	router.Use(middleware.CleanPath) // Not sure
	router.Use(middleware.RequestID)
	router.Use(middleware.Timeout(30 * time.Second)) // 30 second timeout

	// router.Mount("/metrics")
	router.Mount("/connector", ConnectorRouter(done))

	log.Error().Msg(http.ListenAndServe(":"+serverPort, router).Error())
}
