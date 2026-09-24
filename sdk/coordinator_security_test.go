package sdk

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSDKAuthenticatesSubmissionAndPolling(t *testing.T) {
	var submits, polls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sdk-test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs":
			submits.Add(1)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"secure-job"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/secure-job":
			polls.Add(1)
			_, _ = w.Write([]byte(`{"id":"secure-job","status":"FINISHED"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "secret-key")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("sdk-test-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := New().SetMode(Cluster).SetCoordinator(server.URL).SetCoordinatorSecurity(CoordinatorSecurity{CACert: ca, APIKeyFile: key})
	env.AddSourceNamed("source", "source-class", nil).AddSinkNamed("sink", "sink-class", nil)
	exported, err := env.ExportSubmission("secure")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Graph []byte `json:"graph_bytes"`
	}
	if err := json.Unmarshal(exported, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{ca, key, "sdk-test-key"} {
		if bytes.Contains(exported, []byte(secret)) || bytes.Contains(envelope.Graph, []byte(secret)) {
			t.Fatal("client credentials leaked into exported graph")
		}
	}
	result, err := env.Execute(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.JobID != "secure-job" || submits.Load() != 1 || polls.Load() != 1 {
		t.Fatalf("result=%+v submits=%d polls=%d", result, submits.Load(), polls.Load())
	}
}
func TestSDKRefusesAuthenticatedPlaintextBeforeSubmission(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	env := New().SetMode(Cluster).SetCoordinator(server.URL).SetCoordinatorSecurity(CoordinatorSecurity{APIKeyFile: "not-read-until-execution"})
	env.AddSourceNamed("source", "source-class", nil).AddSinkNamed("sink", "sink-class", nil)
	if _, err := env.ExportSubmission("offline"); err != nil {
		t.Fatalf("offline export accessed credential file: %v", err)
	}
	if _, err := env.Execute(t.Context()); err == nil {
		t.Fatal("allowed authenticated plaintext")
	}
	if requests.Load() != 0 {
		t.Fatal("sent insecure request")
	}
}
func TestSDKDoesNotReplaySubmissionThroughRedirect(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	env := New().SetMode(Cluster).SetCoordinator(server.URL)
	env.AddSourceNamed("source", "source-class", nil).AddSinkNamed("sink", "sink-class", nil)
	if _, err := env.Execute(t.Context()); err == nil {
		t.Fatal("redirect accepted as successful submission")
	}
	if forwarded.Load() != 0 {
		t.Fatal("replayed mutation at redirected host")
	}
}
