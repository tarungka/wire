package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
)

func assertJSONError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("non-JSON response: %s", response.Body.String())
	}
	if response.Code != status || body.Error != code || body.Message == "" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestCheckpointAndSavepointUnavailableUseJSONErrors(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
	raw, err := protocol.EncodeMsgPack(TaskAssignmentMap{JobID: "job", Assignments: map[string]string{"task": "worker"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(JobAssignmentsKey("job"), raw); err != nil {
		t.Fatal(err)
	}
	server := &HTTPServer{coord: c}
	for _, handler := range []http.HandlerFunc{server.handleTriggerCheckpoint, server.handleTriggerSavepoint} {
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.SetPathValue("job_id", "job")
		response := httptest.NewRecorder()
		handler(response, request)
		assertJSONError(t, response, 503, "CHECKPOINT_UNAVAILABLE")
	}
	if c.jobs["job"].CheckpointAttempts != 0 || len(c.activeCheckpoints) != 0 {
		t.Fatal("unavailable request mutated checkpoint state")
	}
}

func TestCheckpointLookupJSONErrors(t *testing.T) {
	c, store := newTestCoordinator(t)
	server := &HTTPServer{coord: c}
	for _, id := range []string{"0", "-1", "abc", "18446744073709551616", "1"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.SetPathValue("job_id", "job")
		request.SetPathValue("checkpoint_id", id)
		response := httptest.NewRecorder()
		server.handleGetCheckpoint(response, request)
		if id == "1" {
			assertJSONError(t, response, 404, "CHECKPOINT_NOT_FOUND")
		} else {
			assertJSONError(t, response, 400, "INVALID_REQUEST")
		}
	}
	if err := store.Set(CheckpointKey("job", 1), []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.SetPathValue("job_id", "job")
	request.SetPathValue("checkpoint_id", "1")
	response := httptest.NewRecorder()
	server.handleGetCheckpoint(response, request)
	assertJSONError(t, response, 500, "INTERNAL_ERROR")
}
