package worker

import (
	"crypto/tls"
	"fmt"
)

// PeerTLSConfig serves both sides of worker data and checkpoint connections.
// Require explicit mutual trust; enabling peer TLS must never silently leave
// client authentication or server verification disabled.
func validatePeerTLS(config *tls.Config) error {
	if config == nil {
		return nil
	}
	if config.InsecureSkipVerify || config.ClientAuth != tls.RequireAndVerifyClientCert || config.RootCAs == nil || config.ClientCAs == nil || len(config.Certificates) == 0 {
		return fmt.Errorf("worker peer TLS requires a certificate, server/client CA roots, verified client certificates and server verification")
	}
	return nil
}
