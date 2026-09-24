package coordinator

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func (s *HTTPServer) handleCheckpointInterval(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var request struct {
		Interval *string `json:"interval"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Interval == nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "provide checkpoint interval as a duration string")
		return
	}
	interval, err := time.ParseDuration(*request.Interval)
	if err != nil || interval < 0 || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid checkpoint interval")
		return
	}
	job, err := s.coord.SetCheckpointInterval(r.PathValue("job_id"), interval)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDetailFromMeta(job))
}
