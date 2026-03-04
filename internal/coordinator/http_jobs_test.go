package coordinator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestHTTP_SubmitJob(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	body := `{"name":"test-job","parallelism":2,"config":"sources:\n  - kafka"}`
	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/jobs", srv.Addr()),
		"application/json",
		bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatalf("POST /api/v1/jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var result jobDetailResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Name != "test-job" {
		t.Fatalf("expected name test-job, got %s", result.Name)
	}
	if result.Status != "CREATED" {
		t.Fatalf("expected CREATED, got %s", result.Status)
	}
}

func TestHTTP_SubmitJob_InvalidBody(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/jobs", srv.Addr()),
		"application/json",
		bytes.NewBufferString("not json"),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHTTP_SubmitBinary_NotImplemented(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/jobs/submit", srv.Addr()),
		"application/octet-stream",
		nil,
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", resp.StatusCode)
	}
}

func TestHTTP_ListJobs(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	c.SubmitJob("a", 1, []byte("cfg"))
	c.SubmitJob("b", 1, []byte("cfg"))

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/jobs", srv.Addr()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result jobListResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(result.Jobs))
	}
}

func TestHTTP_ListJobs_StatusFilter(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	c.SubmitJob("a", 1, []byte("cfg"))

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/jobs?status=RUNNING", srv.Addr()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var result jobListResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Jobs) != 0 {
		t.Fatalf("expected 0 RUNNING jobs, got %d", len(result.Jobs))
	}
}

func TestHTTP_GetJob(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	job, _ := c.SubmitJob("j1", 2, []byte("cfg"))

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/jobs/%s", srv.Addr(), job.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result jobDetailResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.ID != job.ID {
		t.Fatalf("expected ID %s, got %s", job.ID, result.ID)
	}
}

func TestHTTP_GetJob_NotFound(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/jobs/nonexistent", srv.Addr()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHTTP_CancelJob(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	c.transitionJob(job, JobDeploying)
	c.transitionJob(job, JobRunning)

	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/jobs/%s/cancel", srv.Addr(), job.ID),
		"", nil,
	)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result jobDetailResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Status != "CANCELING" {
		t.Fatalf("expected CANCELING, got %s", result.Status)
	}
}

func TestHTTP_CancelJob_AlreadyCanceled(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	c.transitionJob(job, JobDeploying)
	c.transitionJob(job, JobRunning)
	c.transitionJob(job, JobCanceling)
	c.transitionJob(job, JobCanceled)

	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/jobs/%s/cancel", srv.Addr(), job.ID),
		"", nil,
	)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

func TestHTTP_PauseResumeJob(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	c.transitionJob(job, JobDeploying)
	c.transitionJob(job, JobRunning)

	// Pause
	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/jobs/%s/pause", srv.Addr(), job.ID),
		"", nil,
	)
	if err != nil {
		t.Fatalf("POST pause: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for pause, got %d", resp.StatusCode)
	}

	// Resume
	resp2, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/jobs/%s/resume", srv.Addr(), job.ID),
		"", nil,
	)
	if err != nil {
		t.Fatalf("POST resume: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for resume, got %d", resp2.StatusCode)
	}

	var result jobDetailResponse
	json.NewDecoder(resp2.Body).Decode(&result)
	if result.Status != "DEPLOYING" {
		t.Fatalf("expected DEPLOYING after resume, got %s", result.Status)
	}
}
