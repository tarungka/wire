package server

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)


func Init(config map[string]interface{}){}

func Run(){


	router := chi.NewRouter()

	router.Use(middleware.Recoverer)
	router.Use(middleware.Logger)
	router.Use(middleware.CleanPath) // Not sure
	router.Use(middleware.RequestID)

	// router.Mount("/metrics")
	router.Mount("/connector", ConnectorRouter())

}