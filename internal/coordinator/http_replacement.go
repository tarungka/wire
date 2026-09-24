package coordinator

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
)

func (s *HTTPServer) handleValidateReplacement(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var request struct {
		Name        string `json:"name"`
		Parallelism int    `json:"parallelism"`
		GraphBytes  string `json:"graph_bytes"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.GraphBytes == "" || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "provide a structured replacement graph")
		return
	}
	graph, err := base64.StdEncoding.DecodeString(request.GraphBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid graph encoding")
		return
	}
	if err := s.coord.ValidateReplacementLayout(r.PathValue("job_id"), request.Parallelism, graph); err != nil {
		writeJobError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
