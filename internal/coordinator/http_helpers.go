package coordinator

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// errorResponse is the standard JSON error format.
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// jobResponse is the API representation of a job.
type jobResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Parallelism int    `json:"parallelism"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// jobDetailResponse includes full job details.
type jobDetailResponse struct {
	jobResponse
	StartedAt        string `json:"started_at,omitempty"`
	FinishedAt       string `json:"finished_at,omitempty"`
	RestartCount     int    `json:"restart_count"`
	LatestCheckpoint uint64 `json:"latest_checkpoint"`
	SavepointPath    string `json:"savepoint_path,omitempty"`
}

// pauseJobResponse includes the job and the savepoint created on pause.
type pauseJobResponse struct {
	Job       jobDetailResponse `json:"job"`
	Savepoint savepointResponse `json:"savepoint"`
}

// jobListResponse wraps a list of jobs.
type jobListResponse struct {
	Jobs []jobResponse `json:"jobs"`
}

// savepointResponse is the API representation of a savepoint.
type savepointResponse struct {
	ID             string `json:"id"`
	JobID          string `json:"job_id"`
	Status         string `json:"status"`
	Path           string `json:"path,omitempty"`
	TriggerTime    string `json:"trigger_time"`
	CompletionTime string `json:"completion_time,omitempty"`
}

// nodeResponse is the API representation of a worker node.
type nodeResponse struct {
	ID                 string   `json:"id"`
	Address            string   `json:"address"`
	TaskSlotsTotal     int      `json:"task_slots_total"`
	TaskSlotsAvailable int      `json:"task_slots_available"`
	LastHeartbeat      string   `json:"last_heartbeat"`
	RunningTasks       []string `json:"running_tasks"`
}

// clusterStatusResponse contains the cluster status.
type clusterStatusResponse struct {
	Leader  *leaderResponse `json:"leader"`
	Workers []nodeResponse  `json:"workers"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers already sent; best-effort logging.
		log.Printf("writeJSON: encode error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: code, Message: message})
}

func writeJobError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrJobNotFound):
		writeError(w, http.StatusNotFound, "JOB_NOT_FOUND", err.Error())
	case errors.Is(err, ErrInvalidTransition):
		writeError(w, http.StatusConflict, "INVALID_TRANSITION", err.Error())
	case errors.Is(err, ErrJobNotRunning):
		writeError(w, http.StatusConflict, "JOB_NOT_RUNNING", err.Error())
	case errors.Is(err, ErrJobNotPaused):
		writeError(w, http.StatusConflict, "JOB_NOT_PAUSED", err.Error())
	case errors.Is(err, ErrNotLeader):
		writeError(w, http.StatusServiceUnavailable, "NOT_LEADER", err.Error())
	case errors.Is(err, ErrJobExists):
		writeError(w, http.StatusConflict, "JOB_EXISTS", err.Error())
	case errors.Is(err, ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, "INVALID_CONFIG", err.Error())
	case errors.Is(err, ErrSavepointNotFound):
		writeError(w, http.StatusNotFound, "SAVEPOINT_NOT_FOUND", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func parseJobStatus(s string) (JobStatus, error) {
	switch strings.ToUpper(s) {
	case "CREATED":
		return JobCreated, nil
	case "DEPLOYING":
		return JobDeploying, nil
	case "RUNNING":
		return JobRunning, nil
	case "FINISHING":
		return JobFinishing, nil
	case "FINISHED":
		return JobFinished, nil
	case "FAILING":
		return JobFailing, nil
	case "FAILED":
		return JobFailed, nil
	case "CANCELING":
		return JobCanceling, nil
	case "CANCELED":
		return JobCanceled, nil
	case "PAUSED":
		return JobPaused, nil
	default:
		return 0, fmt.Errorf("unknown job status: %s", s)
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func jobResponseFromMeta(j *JobMeta) jobResponse {
	return jobResponse{
		ID:          j.ID,
		Name:        j.Name,
		Status:      j.Status.String(),
		Parallelism: j.Parallelism,
		CreatedAt:   formatTime(j.CreatedAt),
		UpdatedAt:   formatTime(j.UpdatedAt),
	}
}

func jobDetailFromMeta(j *JobMeta) jobDetailResponse {
	return jobDetailResponse{
		jobResponse:      jobResponseFromMeta(j),
		StartedAt:        formatTime(j.StartedAt),
		FinishedAt:       formatTime(j.FinishedAt),
		RestartCount:     j.RestartCount,
		LatestCheckpoint: j.LatestCheckpoint,
		SavepointPath:    j.SavepointPath,
	}
}

func savepointResponseFromMeta(sp *SavepointMeta) savepointResponse {
	return savepointResponse{
		ID:             sp.ID,
		JobID:          sp.JobID,
		Status:         sp.Status.String(),
		Path:           sp.Path,
		TriggerTime:    formatTime(sp.TriggerTime),
		CompletionTime: formatTime(sp.CompletionTime),
	}
}
