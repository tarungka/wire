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
		Expected *string `json:"expected_interval,omitempty"`
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
	var expected *time.Duration
	if request.Expected != nil {
		parsed, err := time.ParseDuration(*request.Expected)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid expected interval")
			return
		}
		expected = &parsed
	}
	job, err := s.coord.setCheckpointInterval(r.PathValue("job_id"), interval, expected)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDetailFromMeta(job))
}
