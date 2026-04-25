package coordinator

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// submitJobRequest is the JSON body for POST /api/v1/jobs.
//
// Exactly one of Config or GraphBytes should be populated:
//   - GraphBytes: base64-encoded msgpack rpc.JobGraph (preferred; produced by
//     the SDK's clusterExecutor). Persisted verbatim as the job's config
//     bytes, then parsed by the scheduler to produce task descriptors.
//   - Config: arbitrary opaque bytes (legacy path; ignored by the scheduler).
type submitJobRequest struct {
	Name        string `json:"name"`
	Parallelism int    `json:"parallelism"`
	Config      string `json:"config,omitempty"`
	GraphBytes  string `json:"graph_bytes,omitempty"`
}

func (s *HTTPServer) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20) // 4 MiB limit
	var req submitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}

	var configBytes []byte
	switch {
	case req.GraphBytes != "":
		decoded, err := base64.StdEncoding.DecodeString(req.GraphBytes)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "graph_bytes is not valid base64")
			return
		}
		configBytes = decoded
	case req.Config != "":
		configBytes = []byte(req.Config)
	}

	job, err := s.coord.SubmitJob(req.Name, req.Parallelism, configBytes)
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
		st, err := parseJobStatus(statusStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_STATUS", err.Error())
			return
		}
		filter = &st
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
	job, sp, err := s.coord.PauseJob(jobID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pauseJobResponse{
		Job:       jobDetailFromMeta(job),
		Savepoint: savepointResponseFromMeta(sp),
	})
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
