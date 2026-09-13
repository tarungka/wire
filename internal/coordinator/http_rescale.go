package coordinator

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/tarungka/wire/internal/keygroup"
)

func (s *HTTPServer) handleRescaleJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request struct {
		SavepointID string `json:"savepoint_id"`
		Parallelism int    `json:"parallelism"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid rescale request")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || request.SavepointID == "" || request.Parallelism < 1 || request.Parallelism > keygroup.MaxKeyGroups {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "provide savepoint_id and a valid parallelism")
		return
	}
	job, err := s.coord.RescaleJob(r.PathValue("job_id"), request.SavepointID, request.Parallelism)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobDetailFromMeta(job))
}
