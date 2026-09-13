package coordinator

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPRescaleRejectsInvalidRequestBeforeMutation(t *testing.T) {
	for _, body := range []string{`{}`, `{"savepoint_id":"s","parallelism":0}`, `{"savepoint_id":"s","parallelism":32769}`, `{"savepoint_id":"s","parallelism":3,"typo":true}`, `{"savepoint_id":"s","parallelism":3} {}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			// A nil coordinator proves invalid bodies never reach job mutation.
			server := &HTTPServer{}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job/rescale", strings.NewReader(body))
			response := httptest.NewRecorder()
			server.handleRescaleJob(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
