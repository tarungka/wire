package transport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// LoadTLSConfig creates a server-side TLS configuration.
func LoadTLSConfig(certFile, keyFile string, verifyClient bool, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("transport: failed to load TLS certificate: %w", err)
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	if verifyClient {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		if caFile != "" {
			pool, err := loadCACertPool(caFile)
			if err != nil {
				return nil, err
			}
			cfg.ClientCAs = pool
		}
	}

	return cfg, nil
}

// NewTLSClientConfig creates a client-side TLS configuration.
func NewTLSClientConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("transport: failed to load client TLS certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	if caFile != "" {
		pool, err := loadCACertPool(caFile)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}

	return cfg, nil
}

func loadCACertPool(caFile string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("transport: failed to read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("transport: failed to parse CA certificate from %s", caFile)
	}
	return pool, nil
}
