package coordinator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestAuthFileRejectsAmbiguousIdentitiesAndInvalidKeys(t *testing.T) {
	key := "wk_live_" + strings.Repeat("a", 32)
	for name, users := range map[string][]apiUser{
		"empty username":        {{Role: "admin", APIKey: key}},
		"long username":         {{Username: strings.Repeat("a", 129), Role: "admin", APIKey: key}},
		"duplicate username":    {{Username: "same", Role: "admin", APIKey: key}, {Username: "same", Role: "viewer", APIKey: "wk_live_" + strings.Repeat("b", 32)}},
		"duplicate key":         {{Username: "one", Role: "admin", APIKey: key}, {Username: "two", Role: "viewer", APIKey: key}},
		"invalid key character": {{Username: "user", Role: "viewer", APIKey: "wk_live_" + strings.Repeat("a", 31) + "!"}},
		"both credentials":      {{Username: "user", Role: "viewer", APIKey: key, PasswordHash: "also-present"}},
		"too many users":        make([]apiUser, 101),
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"users": users})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readAPIAuth(bytes.NewReader(data)); err == nil {
				t.Fatal("accepted invalid authentication file")
			}
		})
	}
}

func TestAuthFileFailureDoesNotInstallPartialPolicy(t *testing.T) {
	for _, kind := range []string{"directory", "oversized", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			if kind != "directory" {
				path = filepath.Join(path, "auth.json")
				contents := []byte(`{"users":[]}`)
				if kind == "oversized" {
					contents = bytes.Repeat([]byte(" "), (1<<20)+1)
				}
				if err := os.WriteFile(path, contents, 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := NewHTTPServer(nil, "", zerolog.Nop())
			s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			if err := s.ConfigureAuth(path); err == nil {
				t.Fatal("accepted unreadable or invalid auth file")
			}
			recorder := httptest.NewRecorder()
			s.server.Handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/jobs", nil))
			if recorder.Code != http.StatusNoContent {
				t.Fatal("failed configuration changed the installed handler")
			}
		})
	}
}

func TestAuthAuditUsesPeerWithoutTrustingForwardedHeader(t *testing.T) {
	var output bytes.Buffer
	s := &HTTPServer{log: zerolog.New(&output)}
	handler := s.authenticate(testAPIAuth(t), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("anonymous request reached handler") }))
	request := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	request.RemoteAddr = "unix-peer"
	request.Header.Set("X-Forwarded-For", "untrusted-claimed-origin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("anonymous request admitted")
	}
	var audit map[string]any
	if err := json.Unmarshal(output.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	if audit["source_ip"] != "unix-peer" || bytes.Contains(output.Bytes(), []byte("untrusted-claimed-origin")) {
		t.Fatal("audit trusted a client-supplied origin")
	}
}
