package main

import (
	"crypto/tls"
	"fmt"

	"github.com/tarungka/wire/internal/config"
	"github.com/tarungka/wire/internal/transport"
)

func coordinatorRPCTLS(cfg config.TLSConfig) (*tls.Config, error) {
	if cfg.Cert == "" && cfg.Key == "" {
		if cfg.VerifyClient || cfg.CACert != "" || cfg.VerifyServerName != "" {
			return nil, fmt.Errorf("node TLS server certificate and key required")
		}
		return nil, nil
	}
	return transport.LoadTLSConfig(cfg.Cert, cfg.Key, cfg.VerifyClient, cfg.CACert)
}
func workerRPCTLS(cfg config.TLSConfig) (*tls.Config, error) {
	if (cfg.Cert == "") != (cfg.Key == "") || (cfg.VerifyClient && cfg.Cert == "") {
		return nil, fmt.Errorf("node TLS client certificate and key required")
	}
	if cfg.Cert == "" && cfg.Key == "" && cfg.CACert == "" && cfg.VerifyServerName == "" {
		return nil, nil
	}
	result, err := transport.NewTLSClientConfig(cfg.Cert, cfg.Key, cfg.CACert)
	if err != nil {
		return nil, err
	}
	result.ServerName = cfg.VerifyServerName
	return result, nil
}
