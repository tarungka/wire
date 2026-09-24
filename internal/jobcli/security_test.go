package jobcli

import (
	"bytes"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIUsesPrivateCAAndAPIKey(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[]}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "key")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("test-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	if err := Run(t.Context(), []string{"jobs", "list", "--coordinator", server.URL, "--ca-cert", ca, "--api-key-file", key}, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !bytes.Contains(output.Bytes(), []byte(`"jobs"`)) {
		t.Fatalf("calls=%d output=%s", calls, output.Bytes())
	}
	if bytes.Contains(output.Bytes(), []byte("test-secret")) || bytes.Contains(diagnostics.Bytes(), []byte("test-secret")) {
		t.Fatal("credential exposed")
	}
}
