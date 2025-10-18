package tcp

import (
	"crypto/tls"
	// "fmt"
	"net"
	"time"

	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
)

// NewDialer returns an initialized Dialer.
func NewDialer(header byte, tlsConfig *tls.Config) *Dialer {
	newLogger := logger.GetLogger("tcp")
	newLogger.Printf("creating a new dialer with header: %v", header)
	return &Dialer{
		header:    header,
		tlsConfig: tlsConfig,
		logger:    newLogger,
	}
}

// Dialer supports dialing a cluster service.
type Dialer struct {
	header    byte
	tlsConfig *tls.Config
	logger    zerolog.Logger
}

// Dial dials the cluster service at the given addr and returns a connection.
func (d *Dialer) Dial(addr string, timeout time.Duration) (conn net.Conn, retErr error) {
	// TODO: Implementation truncated
	return nil, nil
}
