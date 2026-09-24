package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestReloadReconcilesLostReplacementResponse(t *testing.T) {
	for _, scenario := range []string{"accepted", "rollback", "not-accepted", "other-request"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			var requestID string
			var mutations, reads int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method + " " + r.URL.Path {
				case "POST /api/v1/jobs/job/replacement/validate":
					w.WriteHeader(http.StatusNoContent)
				case "POST /api/v1/jobs/job/savepoints":
					w.WriteHeader(http.StatusAccepted)
					_, _ = fmt.Fprint(w, `{"id":"save","job_id":"job","status":"COMPLETED"}`)
				case "POST /api/v1/jobs/job/replacement":
					mutations++
					var body struct {
						RequestID string `json:"replacement_request_id"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.RequestID == "" {
						t.Error("replacement has no correlation ID")
					}
					requestID = body.RequestID
					// Simulate a fully received mutation followed by a lost HTTP reply.
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				case "GET /api/v1/jobs/job":
					reads++
					reported := requestID
					if scenario == "not-accepted" {
						reported = ""
					}
					if scenario == "other-request" {
						reported = "another-controller"
					}
					failure := ""
					if scenario == "rollback" {
						failure = "replacement failed"
					}
					status := "RUNNING"
					if scenario == "accepted" {
						status = "FINISHED"
						if reads == 1 {
							status = "FINISHING"
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "job", "status": status, "replacement_request_id": reported, "rescale_failure": failure})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			definition := yamlPipelineHeader + "  sinks:\n    - {name: output, type: test-sink, input: input}\n"
			pipeline, err := ParsePipelineYAML([]byte(definition), PipelineConnectors{NamedSources: map[string]string{"test-source": "source"}, NamedSinks: map[string]string{"test-sink": "sink"}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := pipeline.SetCoordinator(server.URL).Reload(t.Context(), "job")
			if (err == nil) != (scenario == "accepted") {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if errors.Is(err, ErrPipelineReplacementRolledBack) != (scenario == "rollback") {
				t.Fatalf("wrong rollback outcome: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if mutations != 1 || result.ReplacementRequestID != requestID || result.SavepointID != "save" {
				t.Fatalf("mutation count=%d result=%+v request=%s", mutations, result, requestID)
			}
		})
	}
}
