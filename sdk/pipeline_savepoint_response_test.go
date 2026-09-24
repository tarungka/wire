package sdk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestReloadMissingSavepointResponseDoesNotReplace(t *testing.T) {
	for _, scenario := range []string{"not-persisted", "wrong-identity", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			var requested string
			posts, reads := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.URL.Path == "/api/v1/jobs/job/replacement/validate":
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/job/savepoints":
					posts++
					var body struct {
						ID string `json:"savepoint_id"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					requested = body.ID
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/job/savepoints/"+requested:
					reads++
					if scenario == "not-persisted" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					id, status := requested, "FAILED"
					if scenario == "wrong-identity" {
						id, status = "other", "COMPLETED"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "job_id": "job", "status": status})
				default:
					t.Errorf("unexpected mutation/request: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer server.Close()
			definition := yamlPipelineHeader + "  sinks:\n    - {name: output, type: test-sink, input: input}\n"
			pipeline, err := ParsePipelineYAML([]byte(definition), PipelineConnectors{NamedSources: map[string]string{"test-source": "source"}, NamedSinks: map[string]string{"test-sink": "sink"}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := pipeline.SetCoordinator(server.URL).Reload(t.Context(), "job")
			mu.Lock()
			defer mu.Unlock()
			if err == nil || posts != 1 || reads != 1 || result.SavepointID != requested || result.ReplacementRequestID != "" {
				t.Fatalf("result=%+v err=%v posts=%d reads=%d", result, err, posts, reads)
			}
		})
	}
}
