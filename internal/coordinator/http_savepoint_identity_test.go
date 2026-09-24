package coordinator

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPIdentifiedSavepointValidation(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	srv := startTestHTTPServer(t, c)
	endpoint := "http://" + srv.Addr() + "/api/v1/jobs/job/savepoints"
	for _, body := range []string{`{"unknown":true}`, `{"savepoint_id":"../bad"}`, `{"savepoint_id":"sp-123"}`, `{} {}`, strings.Repeat(" ", 1025) + "{}"} {
		resp, err := http.Post(endpoint, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid body accepted: status=%d", resp.StatusCode)
		}
	}
	points, err := c.ListSavepoints("job")
	if err != nil || len(points) != 0 {
		t.Fatalf("invalid input mutated queue: %v %v", points, err)
	}
	id := generateSavepointID()
	for range 2 {
		resp, err := http.Post(endpoint, "application/json", strings.NewReader(`{"savepoint_id":"`+id+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		var saved savepointResponse
		err = json.NewDecoder(resp.Body).Decode(&saved)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusAccepted || saved.ID != id || !saved.Queued {
			t.Fatalf("identity not accepted: %+v status=%d err=%v", saved, resp.StatusCode, err)
		}
	}
	points, err = c.ListSavepoints("job")
	if err != nil || len(points) != 1 {
		t.Fatalf("duplicate creation: %v %v", points, err)
	}
}
