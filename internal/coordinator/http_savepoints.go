package coordinator

import "net/http"

func (s *HTTPServer) handleTriggerSavepoint(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	sp, err := s.coord.TriggerSavepoint(jobID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, savepointResponseFromMeta(sp))
}

func (s *HTTPServer) handleGetSavepoint(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	spID := r.PathValue("savepoint_id")
	sp, err := s.coord.GetSavepoint(jobID, spID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, savepointResponseFromMeta(sp))
}

func (s *HTTPServer) handleListSavepoints(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	sps, err := s.coord.ListSavepoints(jobID)
	if err != nil {
		writeJobError(w, err)
		return
	}

	resp := make([]savepointResponse, 0, len(sps))
	for _, sp := range sps {
		resp = append(resp, savepointResponseFromMeta(sp))
	}
	writeJSON(w, http.StatusOK, map[string]any{"savepoints": resp})
}

func (s *HTTPServer) handleDeleteSavepoint(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	spID := r.PathValue("savepoint_id")
	if err := s.coord.DeleteSavepoint(jobID, spID); err != nil {
		writeJobError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
