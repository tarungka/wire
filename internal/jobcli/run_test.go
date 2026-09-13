package jobcli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		method, path string
	}{
		{[]string{"jobs", "list", "--status", "RUNNING"}, "GET", "/api/v1/jobs?status=RUNNING"},
		{[]string{"jobs", "get", "job-1"}, "GET", "/api/v1/jobs/job-1"},
		{[]string{"jobs", "cancel", "job-1"}, "POST", "/api/v1/jobs/job-1/cancel"},
		{[]string{"savepoints", "get", "job-1", "sp-1"}, "GET", "/api/v1/jobs/job-1/savepoints/sp-1"},
		{[]string{"savepoints", "delete", "job-1", "sp-1"}, "DELETE", "/api/v1/jobs/job-1/savepoints/sp-1"},
		{[]string{"cluster", "status"}, "GET", "/api/v1/cluster"},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.RequestURI() != tc.path {
					t.Errorf("request: %s %s", r.Method, r.URL.RequestURI())
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer server.Close()
			var out bytes.Buffer
			args := append(append([]string(nil), tc.args...), "--coordinator", server.URL)
			if err := Run(context.Background(), args, &out, io.Discard); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"ok": true`) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestServerErrorAndRedirectAreNotSuccess(t *testing.T) {
	for _, code := range []int{http.StatusConflict, http.StatusTemporaryRedirect} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(code)
			_, _ = io.WriteString(w, `{"error":"not leader"}`)
		}))
		var out bytes.Buffer
		err := Run(context.Background(), []string{"jobs", "cancel", "job", "--coordinator", server.URL}, &out, io.Discard)
		server.Close()
		if err == nil || out.Len() != 0 {
			t.Fatalf("code=%d err=%v output=%s", code, err, out.String())
		}
	}
}

func TestSubmissionAndCanceledRequest(t *testing.T) {
	payload := `{"name":"example","parallelism":1,"graph_bytes":"YWJj"}`
	file := filepath.Join(t.TempDir(), "job.json")
	if err := os.WriteFile(file, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Method != "POST" || r.URL.Path != "/api/v1/jobs" || string(body) != payload || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("invalid submission: %s %s %s", r.Method, r.URL.Path, body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"job-1"}`)
	}))
	defer server.Close()
	args := []string{"jobs", "submit", "--file", file, "--coordinator", server.URL}
	var out bytes.Buffer
	if err := Run(context.Background(), args, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, args, io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := os.WriteFile(file, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), args, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted invalid JSON")
	}
}
