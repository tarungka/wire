package coordinator

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type apiUser struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	APIKey       string `json:"api_key"`
	Role         string `json:"role"`
	keyHash      [32]byte
}

type apiAuth struct {
	users     []apiUser
	dummyHash []byte
	mu        sync.Mutex
	tokens    float64
	last      time.Time
}

// ConfigureAuth loads immutable credentials before Listen/Serve. An empty path
// leaves development-mode access unchanged. No partially parsed file is installed.
func (s *HTTPServer) ConfigureAuth(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open authentication file: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("read authentication file: %w", err)
	}
	if len(data) > 1<<20 {
		return errors.New("authentication file exceeds 1 MiB")
	}
	a, err := readAPIAuth(bytes.NewReader(data))
	if err != nil {
		return err
	}
	s.server.Handler = s.authenticate(a, s.server.Handler)
	return nil
}

func readAPIAuth(r io.Reader) (*apiAuth, error) {
	var file struct {
		Users []apiUser `json:"users"`
	}
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, errors.New("invalid authentication JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("authentication file must contain one JSON document")
	}
	if len(file.Users) == 0 || len(file.Users) > 100 {
		return nil, errors.New("authentication file requires 1–100 users")
	}
	names, keys := map[string]bool{}, map[string]bool{}
	for i := range file.Users {
		u := &file.Users[i]
		if u.Username == "" || len(u.Username) > 128 || names[u.Username] {
			return nil, fmt.Errorf("authentication user %d has invalid or duplicate username", i)
		}
		names[u.Username] = true
		if u.Role != "admin" && u.Role != "operator" && u.Role != "viewer" {
			return nil, fmt.Errorf("authentication user %d has invalid role", i)
		}
		if (u.PasswordHash == "") == (u.APIKey == "") {
			return nil, fmt.Errorf("authentication user %d requires exactly one credential", i)
		}
		if u.PasswordHash != "" {
			cost, err := bcrypt.Cost([]byte(u.PasswordHash))
			if err != nil || cost < 10 {
				return nil, fmt.Errorf("authentication user %d requires a bcrypt hash with cost >= 10", i)
			}
		} else {
			if len(u.APIKey) != 40 || !strings.HasPrefix(u.APIKey, "wk_live_") || keys[u.APIKey] {
				return nil, fmt.Errorf("authentication user %d has invalid or duplicate API key", i)
			}
			for _, c := range u.APIKey[8:] {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
					return nil, fmt.Errorf("authentication user %d has invalid API key", i)
				}
			}
			keys[u.APIKey] = true
			u.keyHash = sha256.Sum256([]byte(u.APIKey))
			u.APIKey = ""
		}
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("non-user timing equalizer"), 10)
	if err != nil {
		return nil, err
	}
	return &apiAuth{users: file.Users, dummyHash: dummy, tokens: 20, last: time.Now()}, nil
}

// Global bounded admission prevents password checks from creating unbounded CPU
// work. Public probes bypass it. No client-controlled identity map is retained.
func (a *apiAuth) allow(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens = min(20, a.tokens+max(0, now.Sub(a.last).Seconds())*10)
	a.last = now
	if a.tokens < 1 {
		return false
	}
	a.tokens--
	return true
}

func (a *apiAuth) user(r *http.Request) (apiUser, bool) {
	if len(r.Header.Values("Authorization")) != 1 {
		return apiUser{}, false
	}
	if name, password, ok := r.BasicAuth(); ok {
		var candidate apiUser
		hash := a.dummyHash
		for _, u := range a.users {
			if u.Username == name && u.PasswordHash != "" {
				candidate = u
				hash = []byte(u.PasswordHash)
			}
		}
		err := bcrypt.CompareHashAndPassword(hash, []byte(password))
		return candidate, err == nil && candidate.Username != ""
	}
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return apiUser{}, false
	}
	digest := sha256.Sum256([]byte(token))
	for _, u := range a.users {
		if u.PasswordHash == "" && subtle.ConstantTimeCompare(digest[:], u.keyHash[:]) == 1 {
			return u, true
		}
	}
	return apiUser{}, false
}

func apiRoleAllowed(role, method, path string) bool {
	if role == "admin" {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/jobs") {
		if strings.Contains(path, "/savepoints") {
			return role == "operator"
		}
		if method == http.MethodGet || method == http.MethodHead {
			return true
		}
		return role == "operator"
	}
	return (method == http.MethodGet || method == http.MethodHead) && (path == "/api/v1/cluster" || path == "/api/v1/cluster/leader")
}

func (s *HTTPServer) authenticate(a *apiAuth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics") {
			next.ServeHTTP(w, r)
			return
		}
		if !a.allow(time.Now()) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "authentication rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		u, ok := a.user(r)
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !ok {
			entry := s.log.Warn().Str("source_ip", ip)
			if name, _, basic := r.BasicAuth(); basic && len(name) <= 128 {
				entry = entry.Str("username", name)
			}
			entry.Msg("API authentication failed")
			w.Header().Set("WWW-Authenticate", `Basic realm="wire"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.log.Info().Str("username", u.Username).Str("source_ip", ip).Msg("API authenticated")
		if !apiRoleAllowed(u.Role, r.Method, r.URL.Path) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
