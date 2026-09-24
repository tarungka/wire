package coordinator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

type acceptanceCA struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	pool        *x509.CertPool
}

func newAcceptanceCA(t *testing.T, name string) acceptanceCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return acceptanceCA{certificate: certificate, key: key, pool: pool}
}
func (ca acceptanceCA) issue(t *testing.T, usage x509.ExtKeyUsage, expired bool) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(30 * time.Minute)
	if expired {
		until = time.Now().Add(-time.Minute)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "acceptance-peer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	raw, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}
}

func TestHTTPSCertificateAndCredentialLayers(t *testing.T) {
	ca := newAcceptanceCA(t, "trusted")
	foreign := newAcceptanceCA(t, "untrusted")
	serverCertificate := ca.issue(t, x509.ExtKeyUsageServerAuth, false)
	validClient := ca.issue(t, x509.ExtKeyUsageClientAuth, false)
	expiredClient := ca.issue(t, x509.ExtKeyUsageClientAuth, true)
	foreignClient := foreign.issue(t, x509.ExtKeyUsageClientAuth, false)
	wrongUsage := ca.issue(t, x509.ExtKeyUsageServerAuth, false)
	c, _ := newTestCoordinator(t)
	s := NewHTTPServer(c, "127.0.0.1:0", zerolog.Nop(), &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCertificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: ca.pool})
	key := "wk_live_" + strings.Repeat("a", 32)
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"users":[{"username":"admin","role":"admin","api_key":"`+key+`"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureAuth(path); err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	defer func() { _ = s.Shutdown(context.Background()); <-done }()
	for _, tc := range []struct {
		name        string
		certificate *tls.Certificate
		token       string
		max         uint16
		status      int
	}{
		{"both valid", &validClient, key, tls.VersionTLS13, http.StatusOK},
		{"certificate without API credential", &validClient, "", tls.VersionTLS13, http.StatusUnauthorized},
		{"certificate with invalid API credential", &validClient, "invalid", tls.VersionTLS13, http.StatusUnauthorized},
		{"API credential without certificate", nil, key, tls.VersionTLS13, 0},
		{"expired client certificate", &expiredClient, key, tls.VersionTLS13, 0},
		{"foreign client certificate", &foreignClient, key, tls.VersionTLS13, 0},
		{"wrong certificate usage", &wrongUsage, key, tls.VersionTLS13, 0},
		{"TLS 1.2", &validClient, key, tls.VersionTLS12, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &tls.Config{RootCAs: ca.pool, MinVersion: tls.VersionTLS12, MaxVersion: tc.max}
			if tc.certificate != nil {
				cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return tc.certificate, nil }
			}
			client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
			defer client.CloseIdleConnections()
			req, err := http.NewRequest("GET", "https://"+s.Addr()+"/api/v1/jobs", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response, err := client.Do(req)
			if tc.status == 0 {
				if err == nil {
					response.Body.Close()
					t.Fatal("invalid TLS peer reached HTTP")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
				t.Fatalf("unexpected authenticated HTTPS response: %d", response.StatusCode)
			}
			switch response.TLS.CipherSuite {
			case tls.TLS_AES_128_GCM_SHA256, tls.TLS_AES_256_GCM_SHA384, tls.TLS_CHACHA20_POLY1305_SHA256:
			default:
				t.Fatalf("unexpected cipher suite: %x", response.TLS.CipherSuite)
			}
		})
	}
}

func TestHTTPSRejectsInvalidServerCertificates(t *testing.T) {
	trusted := newAcceptanceCA(t, "server-trust")
	foreign := newAcceptanceCA(t, "foreign-server")
	for _, tc := range []struct {
		name        string
		certificate tls.Certificate
		serverName  string
		allowed     bool
	}{
		{"trusted", trusted.issue(t, x509.ExtKeyUsageServerAuth, false), "", true},
		{"expired", trusted.issue(t, x509.ExtKeyUsageServerAuth, true), "", false},
		{"untrusted", foreign.issue(t, x509.ExtKeyUsageServerAuth, false), "", false},
		{"wrong hostname", trusted.issue(t, x509.ExtKeyUsageServerAuth, false), "other.invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestCoordinator(t)
			s := NewHTTPServer(c, "127.0.0.1:0", zerolog.Nop(), &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{tc.certificate}})
			if err := s.Listen(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- s.Serve() }()
			defer func() { _ = s.Shutdown(context.Background()); <-done }()
			client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trusted.pool, MinVersion: tls.VersionTLS13, ServerName: tc.serverName}}}
			defer client.CloseIdleConnections()
			response, err := client.Get("https://" + s.Addr() + "/healthz")
			if !tc.allowed {
				if err == nil {
					response.Body.Close()
					t.Fatal("accepted invalid server identity")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatal(response.StatusCode)
			}
		})
	}
}

func TestHTTPSClientTrustReplacementRequiresRestart(t *testing.T) {
	oldCA := newAcceptanceCA(t, "old-client-trust")
	replacementCA := newAcceptanceCA(t, "new-client-trust")
	serverCertificate := oldCA.issue(t, x509.ExtKeyUsageServerAuth, false)
	oldClient := oldCA.issue(t, x509.ExtKeyUsageClientAuth, false)
	newClient := replacementCA.issue(t, x509.ExtKeyUsageClientAuth, false)
	start := func(pool *x509.CertPool) (string, func()) {
		t.Helper()
		c, _ := newTestCoordinator(t)
		s := NewHTTPServer(c, "127.0.0.1:0", zerolog.Nop(), &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCertificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool})
		if err := s.Listen(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- s.Serve() }()
		var once sync.Once
		stop := func() { once.Do(func() { _ = s.Shutdown(context.Background()); <-done }) }
		t.Cleanup(stop)
		return "https://" + s.Addr() + "/healthz", stop
	}
	check := func(endpoint string, certificate tls.Certificate, allowed bool) {
		t.Helper()
		cfg := &tls.Config{RootCAs: oldCA.pool, MinVersion: tls.VersionTLS13, GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &certificate, nil }}
		client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
		defer client.CloseIdleConnections()
		response, err := client.Get(endpoint)
		if !allowed {
			if err == nil {
				response.Body.Close()
				t.Fatal("removed client trust still admitted certificate")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatal(response.StatusCode)
		}
	}
	endpoint, stop := start(oldCA.pool)
	check(endpoint, oldClient, true)
	check(endpoint, newClient, false)
	stop()
	endpoint, stop = start(replacementCA.pool)
	defer stop()
	check(endpoint, oldClient, false)
	check(endpoint, newClient, true)
}
