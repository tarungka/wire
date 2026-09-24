package coordinator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestAPIAuthConcurrentAdmissionIsBounded(t *testing.T) {
	now := time.Now()
	a := &apiAuth{tokens: 20, last: now}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.allow(now) {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := admitted.Load(); got != 20 {
		t.Fatalf("admitted %d requests, want burst of 20", got)
	}
	if a.allow(now.Add(99 * time.Millisecond)) {
		t.Fatal("refilled before one token accrued")
	}
	if !a.allow(now.Add(100 * time.Millisecond)) {
		t.Fatal("did not refill one token")
	}
	if a.allow(now.Add(100 * time.Millisecond)) {
		t.Fatal("spent the same replenished token twice")
	}
}

func TestAPIAuthLogsIdentityWithoutCredentials(t *testing.T) {
	a := testAPIAuth(t)
	var logs bytes.Buffer
	s := &HTTPServer{log: zerolog.New(&logs).Level(zerolog.DebugLevel)}
	h := s.authenticate(a, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		name, password, bearer string
		status                 int
	}{
		{name: "admin", password: "password", status: 204},
		{name: "admin", password: "rejected-private-password", status: 401},
		{bearer: "wk_live_" + strings.Repeat("v", 32), status: 204},
		{bearer: "wk_live_" + strings.Repeat("z", 32), status: 401},
	} {
		r := httptest.NewRequest("GET", "/api/v1/jobs", nil)
		r.RemoteAddr = "192.0.2.10:4321"
		if tc.bearer != "" {
			r.Header.Set("Authorization", "Bearer "+tc.bearer)
		} else {
			r.SetBasicAuth(tc.name, tc.password)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status=%d want=%d", w.Code, tc.status)
		}
		for _, secret := range []string{tc.password, tc.bearer, r.Header.Get("Authorization")} {
			if secret != "" && (strings.Contains(logs.String(), secret) || strings.Contains(w.Body.String(), secret)) {
				t.Fatal("credential leaked into log or response")
			}
		}
	}
	dec := json.NewDecoder(&logs)
	for i := range 4 {
		var entry map[string]any
		if err := dec.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry["source_ip"] != "192.0.2.10" {
			t.Fatalf("missing source attribution: %v", entry)
		}
		if i < 2 && entry["username"] != "admin" {
			t.Fatal("missing Basic username")
		}
		if i == 2 && (entry["username"] != "viewer" || entry["level"] != "info") {
			t.Fatal("missing successful API-key audit identity")
		}
	}
}

func TestAPIAuthRejectsAmbiguousCredentials(t *testing.T) {
	a := testAPIAuth(t)
	for _, headers := range [][]string{
		{"Bearer wk_live_" + strings.Repeat("v", 32), "Bearer invalid"},
		{"Bearer invalid", "Bearer wk_live_" + strings.Repeat("v", 32)},
	} {
		r := httptest.NewRequest("GET", "/api/v1/jobs", nil)
		for _, value := range headers {
			r.Header.Add("Authorization", value)
		}
		if _, ok := a.user(r); ok {
			t.Fatal("accepted duplicate Authorization headers")
		}
	}
	for _, u := range a.users {
		if u.APIKey != "" {
			t.Fatal("plaintext API key retained after parsing")
		}
	}
}

// Revocation is deliberately restart-based: a running server keeps an immutable
// credential snapshot, while a replacement server must load the new file.
func TestAPIAuthRevocationOnServerRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	oldKey := "wk_live_" + strings.Repeat("a", 32)
	newKey := "wk_live_" + strings.Repeat("b", 32)
	write := func(key string) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"users": []apiUser{{Username: "viewer", Role: "viewer", APIKey: key}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := func() *HTTPServer {
		t.Helper()
		s := NewHTTPServer(nil, "", zerolog.Nop())
		s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		if err := s.ConfigureAuth(path); err != nil {
			t.Fatal(err)
		}
		return s
	}
	check := func(s *HTTPServer, key string, want int) {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/jobs", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status %d want %d", w.Code, want)
		}
	}
	write(oldKey)
	oldServer := server()
	check(oldServer, oldKey, 204)
	write(newKey)
	check(oldServer, oldKey, 204)
	check(oldServer, newKey, 401)
	replacement := server()
	check(replacement, oldKey, 401)
	check(replacement, newKey, 204)
}
