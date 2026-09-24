package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
)

func testAPIAuth(t *testing.T) *apiAuth {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"users": []apiUser{
		{Username: "admin", PasswordHash: string(hash), Role: "admin"},
		{Username: "operator", APIKey: "wk_live_" + strings.Repeat("o", 32), Role: "operator"},
		{Username: "viewer", APIKey: "wk_live_" + strings.Repeat("v", 32), Role: "viewer"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := readAPIAuth(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAPIAuthEndpointRoles(t *testing.T) {
	a := testAPIAuth(t)
	routes := []struct {
		method, path     string
		viewer, operator bool
	}{
		{"GET", "/api/v1/jobs", true, true}, {"GET", "/api/v1/jobs/j", true, true},
		{"POST", "/api/v1/jobs", false, true}, {"POST", "/api/v1/jobs/submit", false, true},
		{"POST", "/api/v1/jobs/j/cancel", false, true}, {"POST", "/api/v1/jobs/j/pause", false, true},
		{"POST", "/api/v1/jobs/j/resume", false, true}, {"POST", "/api/v1/jobs/j/savepoints", false, true},
		{"GET", "/api/v1/jobs/j/savepoints", false, true}, {"GET", "/api/v1/jobs/j/savepoints/s", false, true},
		{"DELETE", "/api/v1/jobs/j/savepoints/s", false, true}, {"GET", "/api/v1/cluster", true, true},
		{"GET", "/api/v1/cluster/leader", true, true}, {"DELETE", "/api/v1/cluster/nodes/n", false, false},
	}
	s := &HTTPServer{log: zerolog.Nop()}
	h := s.authenticate(a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, route := range routes {
		for _, role := range []string{"admin", "operator", "viewer"} {
			t.Run(role+route.method+route.path, func(t *testing.T) {
				a.mu.Lock()
				a.tokens = 20
				a.mu.Unlock()
				r := httptest.NewRequest(route.method, route.path, nil)
				if role == "admin" {
					r.SetBasicAuth("admin", "password")
				} else {
					r.Header.Set("Authorization", "Bearer wk_live_"+strings.Repeat(role[:1], 32))
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				want := http.StatusForbidden
				if role == "admin" || role == "viewer" && route.viewer || role == "operator" && route.operator {
					want = http.StatusNoContent
				}
				if w.Code != want {
					t.Fatalf("status=%d want=%d", w.Code, want)
				}
			})
		}
	}
}

func TestAPIAuthRejectionPublicAndRateLimit(t *testing.T) {
	a := testAPIAuth(t)
	s := &HTTPServer{log: zerolog.Nop()}
	h := s.authenticate(a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 204 {
			t.Fatalf("public %s: %d", path, w.Code)
		}
	}
	for _, header := range []string{"", "Bearer invalid", "Basic !!!", "Digest invalid"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/jobs", nil)
		r.Header.Set("Authorization", header)
		h.ServeHTTP(w, r)
		if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("invalid credential status=%d", w.Code)
		}
	}
	for _, name := range []string{"admin", "missing"} {
		r := httptest.NewRequest("GET", "/api/v1/jobs", nil)
		r.SetBasicAuth(name, "wrong")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	a.mu.Lock()
	a.tokens = 0
	a.last = time.Now().Add(time.Hour)
	a.mu.Unlock()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/jobs", nil))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("limit status=%d", w.Code)
	}
}

func TestAPIAuthRejectsInvalidFiles(t *testing.T) {
	for _, input := range []string{
		`null`, `{}`, `{"users":[]}`, `{"other":1}`, `{} {}`, `{`,
		`{"users":[{"username":"u","role":"admin"}]}`,
		`{"users":[{"username":"u","role":"root","api_key":"x"}]}`,
		`{"users":[{"username":"u","role":"admin","password_hash":"plaintext"}]}`,
		`{"users":[{"username":"u","role":"admin","api_key":"short"}]}`,
	} {
		if _, err := readAPIAuth(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}

func TestConfigureAuthFailsBeforeReplacingHandler(t *testing.T) {
	s := NewHTTPServer(nil, "", zerolog.Nop())
	if err := s.ConfigureAuth(""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureAuth(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"users":[{"username":"v","role":"viewer","api_key":"wk_live_`+strings.Repeat("v", 32)+`"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureAuth(path); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/jobs", nil))
	if w.Code != 401 {
		t.Fatalf("configured server allowed anonymous request: %d", w.Code)
	}
}
