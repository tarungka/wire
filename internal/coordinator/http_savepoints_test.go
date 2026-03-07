package coordinator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestHTTP_TriggerSavepoint(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transitionJob to deploying: %v", err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transitionJob to running: %v", err)
	}

	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/jobs/%s/savepoints", srv.Addr(), job.ID),
		"", nil,
	)
	if err != nil {
		t.Fatalf("POST savepoints: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	var result savepointResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Status != "IN_PROGRESS" {
		t.Fatalf("expected IN_PROGRESS, got %s", result.Status)
	}
}

func TestHTTP_ListSavepoints(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transitionJob to deploying: %v", err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transitionJob to running: %v", err)
	}

	if _, err := c.TriggerSavepoint(job.ID); err != nil {
		t.Fatalf("TriggerSavepoint: %v", err)
	}
	if _, err := c.TriggerSavepoint(job.ID); err != nil {
		t.Fatalf("TriggerSavepoint: %v", err)
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/jobs/%s/savepoints", srv.Addr(), job.ID))
	if err != nil {
		t.Fatalf("GET savepoints: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Savepoints []savepointResponse `json:"savepoints"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result.Savepoints) != 2 {
		t.Fatalf("expected 2 savepoints, got %d", len(result.Savepoints))
	}
}

func TestHTTP_GetSavepoint(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transitionJob to deploying: %v", err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transitionJob to running: %v", err)
	}

	sp, err := c.TriggerSavepoint(job.ID)
	if err != nil {
		t.Fatalf("TriggerSavepoint: %v", err)
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/jobs/%s/savepoints/%s", srv.Addr(), job.ID, sp.ID))
	if err != nil {
		t.Fatalf("GET savepoint: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result savepointResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.ID != sp.ID {
		t.Fatalf("expected ID %s, got %s", sp.ID, result.ID)
	}
}

func TestHTTP_DeleteSavepoint(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	job, _ := c.SubmitJob("j1", 1, []byte("cfg"))
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatalf("transitionJob to deploying: %v", err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatalf("transitionJob to running: %v", err)
	}

	sp, err := c.TriggerSavepoint(job.ID)
	if err != nil {
		t.Fatalf("TriggerSavepoint: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://%s/api/v1/jobs/%s/savepoints/%s", srv.Addr(), job.ID, sp.ID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE savepoint: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestHTTP_DeleteSavepoint_NotFound(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://%s/api/v1/jobs/job-1/savepoints/sp-nope", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
