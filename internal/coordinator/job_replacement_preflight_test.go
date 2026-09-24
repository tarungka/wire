package coordinator

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
)

func TestReplacementPreflightRejectsLayoutBeforeStopping(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	job := c.jobs["job"]
	job.Parallelism = 2
	job.Config = encode(t, linearGraph())
	assignments, _ := store.Get(JobAssignmentsKey(job.ID))
	for _, tc := range []struct {
		name        string
		parallelism int
		change      func(*rpc.JobGraph)
		valid       bool
	}{
		{"same", 2, func(*rpc.JobGraph) {}, true},
		{"code", 2, func(g *rpc.JobGraph) { g.Operators[0].ClassName = "new-source-code" }, true},
		{"parallelism", 3, func(*rpc.JobGraph) {}, false},
		{"identity", 2, func(g *rpc.JobGraph) { g.Operators[0].OperatorID = "different" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := linearGraph()
			tc.change(&graph)
			err := c.ValidateReplacementLayout(job.ID, tc.parallelism, encode(t, graph))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if job.Status != JobRunning || job.PauseSavepointID != "" || job.UpgradeSuccessorID != "" {
				t.Fatal("preflight mutated predecessor")
			}
			after, _ := store.Get(JobAssignmentsKey(job.ID))
			if string(after) != string(assignments) || len(c.DrainCommands("worker")) != 0 {
				t.Fatal("preflight changed assignment or sent commands")
			}
		})
	}
}

func TestReplacementPreflightHTTP(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	c.jobs["job"].Config = encode(t, linearGraph())
	c.jobs["job"].Parallelism = 2
	server := NewHTTPServer(c, "", zerolog.Nop())
	for _, tc := range []struct {
		name        string
		parallelism int
		expected    int
	}{{"same", 2, 204}, {"changed", 3, 400}} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"name": "candidate", "parallelism": tc.parallelism, "graph_bytes": base64.StdEncoding.EncodeToString(encode(t, linearGraph()))})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job/replacement/validate", bytes.NewReader(data)))
			if response.Code != tc.expected {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if c.jobs["job"].Status != JobRunning || c.jobs["job"].PauseSavepointID != "" {
				t.Fatal("validation stopped job")
			}
		})
	}
}

func TestReplacementHTTPRejectsMalformedMutation(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	c.jobs["job"].Name = "current"
	server := NewHTTPServer(c, "", zerolog.Nop())
	for _, body := range []string{`{}`, `{"savepoint_id":"save","graph_bytes":"invalid"}`, `{"name":"current","savepoint_id":"save","graph_bytes":"eA==","extra":true}`, `{"name":"other","savepoint_id":"save","graph_bytes":"eA=="}`, `{"name":"current","savepoint_id":"save","graph_bytes":"eA=="} {}`} {
		response := httptest.NewRecorder()
		server.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job/replacement", bytes.NewBufferString(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, response.Code)
		}
		if c.jobs["job"].Status != JobRunning || c.jobs["job"].RescaleRollback != nil {
			t.Fatal("rejected request mutated job")
		}
	}
}
