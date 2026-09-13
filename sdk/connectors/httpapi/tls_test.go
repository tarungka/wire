package httpapi

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceTLSBasicAuth(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(SourceConfig{Address: "127.0.0.1:0", CertFile: certFile, KeyFile: keyFile, Auth: Auth{Type: "basic", Username: "user", Password: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(parsed)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "https://"+source.Address()+"/ingest", bytes.NewBufferString(`{"events":[{"value":"secure"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("user", "secret")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("TLS ingest: %d", response.StatusCode)
	}
}
