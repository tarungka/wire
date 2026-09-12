package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestHTTPServerUsesTLS(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	c, _ := newReadyCoordinator(t)
	srv := NewHTTPServer(c, "127.0.0.1:0", zerolog.Nop(), &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	defer func() { _ = srv.Shutdown(context.Background()); <-done }()
	pool := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool.AddCert(parsed)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("https://" + srv.Addr() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("expected TLS1.3 health response: %+v", resp)
	}
	plain := &http.Client{Timeout: time.Second}
	resp, err = plain.Get("http://" + srv.Addr() + "/healthz")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("plaintext request succeeded")
		}
	}
}

func TestHTTPMutualTLSRejectsMissingCertificate(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	c, _ := newReadyCoordinator(t)
	srv := NewHTTPServer(c, "127.0.0.1:0", zerolog.Nop(), &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	defer func() { _ = srv.Shutdown(context.Background()); <-done }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("https://" + srv.Addr() + "/healthz")
	if err == nil {
		resp.Body.Close()
		t.Fatal("mutual TLS accepted a client without a certificate")
	}
}
