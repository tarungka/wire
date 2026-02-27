package transport

import (
	"crypto/tls"
	"io"
	"time"

	"github.com/hashicorp/yamux"
)

// Default transport configuration values per WIP-01 Section 2.2.
const (
	DefaultPort                   = 4002
	DefaultKeepAliveInterval      = 15 * time.Second
	DefaultConnectionWriteTimeout = 10 * time.Second
	DefaultMaxStreamWindowSize    = 1024 * 1024 // 1 MB
	DefaultHandshakeTimeout       = 5 * time.Second
	MaxConsecutiveCRCErrors       = 10
	MaxConsecutiveDecodeErrors    = 10
)

// Config holds transport-level configuration.
type Config struct {
	ListenAddr             string
	TLSConfig              *tls.Config
	MaxFrameSize           uint32
	HandshakeTimeout       time.Duration
	KeepAliveInterval      time.Duration
	ConnectionWriteTimeout time.Duration
	MaxStreamWindowSize    uint32
	LocalProtocolVersion   uint16
	LocalMinVersion        uint16
	LocalFeatures          uint32
}

// DefaultConfig returns a Config populated with default values.
func DefaultConfig() Config {
	return Config{
		ListenAddr:             ":4002",
		MaxFrameSize:           16 * 1024 * 1024,
		HandshakeTimeout:       DefaultHandshakeTimeout,
		KeepAliveInterval:      DefaultKeepAliveInterval,
		ConnectionWriteTimeout: DefaultConnectionWriteTimeout,
		MaxStreamWindowSize:    uint32(DefaultMaxStreamWindowSize),
		LocalProtocolVersion:   1,
		LocalMinVersion:        1,
		LocalFeatures:          0,
	}
}

// yamuxConfig builds a yamux.Config from the Wire transport Config.
func (c Config) yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.KeepAliveInterval = c.KeepAliveInterval
	cfg.ConnectionWriteTimeout = c.ConnectionWriteTimeout
	cfg.MaxStreamWindowSize = c.MaxStreamWindowSize
	cfg.LogOutput = io.Discard // Suppress yamux's default logging.
	return cfg
}
