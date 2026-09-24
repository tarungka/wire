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

func (s *HTTPServer) handleReplaceJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var request struct {
		Name        string `json:"name"`
		Parallelism int    `json:"parallelism"`
		GraphBytes  string `json:"graph_bytes"`
		RequestID   string `json:"replacement_request_id,omitempty"`
		SavepointID string `json:"savepoint_id"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.GraphBytes == "" || request.SavepointID == "" || len(request.RequestID) > 128 || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "provide graph_bytes and savepoint_id")
		return
	}
	graph, err := base64.StdEncoding.DecodeString(request.GraphBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid graph encoding")
		return
	}
	current, err := s.coord.GetJob(r.PathValue("job_id"))
	if err != nil {
		writeJobError(w, err)
		return
	}
	if request.Name != current.Name {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "replacement must preserve the job name")
		return
	}
	job, err := s.coord.replaceJobFromSavepoint(current.ID, request.SavepointID, request.Parallelism, graph, request.RequestID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobDetailFromMeta(job))
}
