package apiclient

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func credentialFile(t *testing.T, name, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestClientHTTPSAuthAndRedirectBoundary(t *testing.T) {
	for _, basic := range []bool{false, true} {
		t.Run(map[bool]string{false: "bearer", true: "basic"}[basic], func(t *testing.T) {
			leaked := false
			other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked = true }))
			defer other.Close()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if basic {
					user, password, ok := r.BasicAuth()
					if !ok || user != "reader" || password != " secret " {
						t.Error("incorrect Basic credentials")
					}
				} else if r.Header.Get("Authorization") != "Bearer secret-key" {
					t.Error("incorrect bearer credentials")
				}
				if r.TLS == nil || r.TLS.Version != 0x0304 {
					t.Error("TLS 1.3 required")
				}
				http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			cfg := Config{CACert: credentialFile(t, "ca.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})))}
			if basic {
				cfg.Username = "reader"
				cfg.PasswordFile = credentialFile(t, "password", " secret \n")
			} else {
				cfg.APIKeyFile = credentialFile(t, "key", "secret-key\n")
			}
			client, err := New(server.URL, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			request, _ := http.NewRequest("GET", server.URL+"/api/v1/jobs", nil)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 307 || leaked {
				t.Fatal("followed authenticated redirect")
			}
			if request.Header.Get("Authorization") != "" {
				t.Fatal("mutated caller request")
			}
			foreign, _ := http.NewRequest("GET", other.URL, nil)
			if _, err := client.Do(foreign); err == nil {
				t.Fatal("accepted foreign origin")
			}
			request.Host = "other.example"
			if _, err := client.Do(request); err == nil {
				t.Fatal("accepted foreign Host")
			}
		})
	}
}
func TestClientRejectsInvalidSecurityConfiguration(t *testing.T) {
	secret := "NEVER-PRINT-THIS"
	for _, value := range []string{"", secret + "\nsecond", secret + "\x00", strings.Repeat("x", 16385), secret + " tab"} {
		cfg := Config{APIKeyFile: credentialFile(t, "key", value)}
		_, err := New("https://localhost", cfg, time.Second)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("invalid credential error=%v", err)
		}
	}
	for _, cfg := range []Config{{Username: "user"}, {PasswordFile: "password"}, {APIKeyFile: "key", Username: "user", PasswordFile: "password"}, {ClientCert: "cert"}, {CACert: "missing"}} {
		if _, err := New("https://localhost", cfg, time.Second); err == nil {
			t.Fatalf("accepted invalid config %+v", cfg)
		}
	}
	if _, err := New("http://localhost", Config{APIKeyFile: "missing"}, time.Second); err == nil {
		t.Fatal("allowed credential over plaintext")
	}
}
