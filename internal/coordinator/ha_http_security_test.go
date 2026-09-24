package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestHAHTTPAuthenticationSurvivesTakeover(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := fixture.TLS.Certificates[0]
	fixture.Close()
	pool := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool.AddCert(parsed)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	dir := t.TempDir()
	auth := filepath.Join(dir, "auth.json")
	const key = "wk_live_01234567890123456789012345678901"
	if err := os.WriteFile(auth, []byte(`{"users":[{"username":"reader","api_key":"`+key+`","role":"viewer"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	start := func(id string) (*HAService, context.CancelFunc, <-chan error) {
		t.Helper()
		election := NewFileLockElection(filepath.Join(dir, "leader.lock"), id)
		t.Cleanup(func() { _ = election.Close() })
		h := NewHAService(CoordinatorConfig{NodeID: id, ListenAddr: "127.0.0.1:0"}, "127.0.0.1:0", election, func() (MetadataStore, error) { return NewPebbleStore(filepath.Join(dir, "metadata")) }, nil, zerolog.Nop())
		config := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
		if err := h.ConfigureHTTP(config, auth); err != nil {
			t.Fatal(err)
		}
		config.Certificates = nil // The caller's later mutation must not remove server credentials.
		if err := h.Listen(); err != nil {
			t.Fatal(err)
		}
		if err := h.ConfigureHTTP(nil, ""); err == nil {
			t.Fatal("changed security after Listen")
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- h.Run(ctx) }()
		t.Cleanup(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("HA shutdown timed out")
			}
		})
		return h, cancel, done
	}
	check := func(h *HAService, method, path, credential string, want int) {
		t.Helper()
		r, err := http.NewRequest(method, "https://"+h.HTTPAddr()+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
			t.Fatalf("%s %s: status=%d want=%d TLS=%v", method, path, response.StatusCode, want, response.TLS)
		}
	}
	first, stopFirst, _ := start("first")
	waitHATerm(t, first)
	second, _, _ := start("second")
	// Both active and standby surfaces require authentication before routing or redirect.
	for _, h := range []*HAService{first, second} {
		check(h, "GET", "/api/v1/jobs", "", 401)
		check(h, "POST", "/api/v1/jobs", key, 403)
		check(h, "GET", "/healthz", "", 200)
	}
	check(first, "GET", "/api/v1/jobs", key, 200)
	request, err := http.NewRequest("GET", "https://"+second.HTTPAddr()+"/api/v1/jobs?limit=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	redirect, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	redirect.Body.Close()
	if redirect.StatusCode != 307 || !strings.HasPrefix(redirect.Header.Get("Location"), "https://") || !strings.HasSuffix(redirect.Header.Get("Location"), "/api/v1/jobs?limit=1") {
		t.Fatalf("insecure/invalid redirect: %d %s", redirect.StatusCode, redirect.Header.Get("Location"))
	}

	plain := &http.Client{Timeout: time.Second}
	response, err := plain.Get("http://" + second.HTTPAddr() + "/healthz")
	if err == nil {
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("plaintext accepted")
		}
	}
	stopFirst()
	waitHATerm(t, second)
	check(second, "GET", "/api/v1/jobs", "", 401)
	check(second, "POST", "/api/v1/jobs", key, 403)
	check(second, "GET", "/api/v1/jobs", key, 200)
}

func TestHAHTTPInvalidAuthDoesNotInstallPartialPolicy(t *testing.T) {
	h := NewHAService(CoordinatorConfig{ListenAddr: "127.0.0.1:0"}, "127.0.0.1:0", nil, nil, nil, zerolog.Nop())
	if err := h.ConfigureHTTP(&tls.Config{MinVersion: tls.VersionTLS13}, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("accepted missing auth file")
	}
	if h.http.TLSConfig != nil || h.httpListener != nil {
		t.Fatal("failed config partially installed")
	}
}
