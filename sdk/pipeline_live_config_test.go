package sdk

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPipelineLiveIntervalClient(t *testing.T) {
	for _, scenario := range []string{"success", "denied", "mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPut || r.URL.Path != "/api/v1/jobs/job/checkpoint-interval" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				var body struct {
					Interval string `json:"interval"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Interval != "2s" {
					t.Errorf("interval=%s", body.Interval)
				}
				switch scenario {
				case "denied":
					w.WriteHeader(http.StatusForbidden)
				case "mismatch":
					_, _ = io.WriteString(w, `{"id":"job","checkpoint_interval":"1s"}`)
				default:
					_, _ = io.WriteString(w, `{"id":"job","checkpoint_interval":"2s"}`)
				}
			}))
			defer server.Close()
			pipeline := (&YAMLPipeline{env: New()}).SetCoordinator(server.URL)
			err := pipeline.UpdateCheckpointInterval(t.Context(), "job", 2*time.Second)
			if (err == nil) != (scenario == "success") || calls.Load() != 1 {
				t.Fatalf("error=%v calls=%d", err, calls.Load())
			}
			if err := pipeline.UpdateCheckpointInterval(t.Context(), "../job", time.Second); err == nil {
				t.Fatal("invalid identifier accepted")
			}
			if calls.Load() != 1 {
				t.Fatal("invalid update reached server")
			}
		})
	}
}
