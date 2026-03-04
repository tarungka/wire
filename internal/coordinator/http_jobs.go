package coordinator

import (
	"encoding/json"
	"net/http"
)

// submitJobRequest is the JSON body for POST /api/v1/jobs.
type submitJobRequest struct {
	Name        string `json:"name"`
	Parallelism int    `json:"parallelism"`
	Config      string `json:"config"`
}

func (s *HTTPServer) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	var req submitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}

	job, err := s.coord.SubmitJob(req.Name, req.Parallelism, []byte(req.Config))
	if err != nil {
		writeJobError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, jobDetailFromMeta(job))
}

func (s *HTTPServer) handleSubmitBinary(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "binary job submission is not yet supported")
}

func (s *HTTPServer) handleListJobs(w http.ResponseWriter, r *http.Request) {
	var filter *JobStatus
	if statusStr := r.URL.Query().Get("status"); statusStr != "" {
		s, err := parseJobStatus(statusStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_STATUS", err.Error())
			return
		}
		filter = &s
	}

	jobs := s.coord.ListJobs(filter)
	resp := jobListResponse{Jobs: make([]jobResponse, 0, len(jobs))}
	for _, j := range jobs {
		resp.Jobs = append(resp.Jobs, jobResponseFromMeta(j))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *HTTPServer) handleGetJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	job, err := s.coord.GetJob(jobID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDetailFromMeta(job))
}

func (s *HTTPServer) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	job, err := s.coord.CancelJob(jobID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDetailFromMeta(job))
}

func (s *HTTPServer) handlePauseJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	job, _, err := s.coord.PauseJob(jobID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDetailFromMeta(job))
}

func (s *HTTPServer) handleResumeJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	job, err := s.coord.ResumeJob(jobID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDetailFromMeta(job))
}
