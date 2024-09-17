package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func ConnectorRouter() chi.Router {
	router := chi.NewRouter()

	router.Get("/{connector_name}", test())

	return router
}
func test() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// w.Write("Hi :)")
	}
}
