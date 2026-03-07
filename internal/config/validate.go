package config

import (
	"errors"
	"fmt"
	"os"
)

// Validate checks the WireConfig for semantic errors. It collects all
// validation errors rather than failing on the first one.
func (c *WireConfig) Validate() error {
	var errs []error

	// TLS cert/key pairing — http.tls
	if err := validateTLSPair("http.tls", &c.HTTP.TLS); err != nil {
		errs = append(errs, err)
	}
	// TLS cert/key pairing — node_tls
	if err := validateTLSPair("node_tls", &c.NodeTLS); err != nil {
		errs = append(errs, err)
	}

	// File existence for TLS paths.
	checkFileExists(&errs, "http.tls.cert", c.HTTP.TLS.Cert)
	checkFileExists(&errs, "http.tls.key", c.HTTP.TLS.Key)
	checkFileExists(&errs, "http.tls.ca_cert", c.HTTP.TLS.CACert)
	checkFileExists(&errs, "node_tls.cert", c.NodeTLS.Cert)
	checkFileExists(&errs, "node_tls.key", c.NodeTLS.Key)
	checkFileExists(&errs, "node_tls.ca_cert", c.NodeTLS.CACert)
	checkFileExists(&errs, "auth.file", c.Auth.File)

	// Write queue bounds.
	if c.WriteQueue.Capacity < 0 {
		errs = append(errs, fmt.Errorf("write_queue.capacity must be >= 0, got %d", c.WriteQueue.Capacity))
	}
	if c.WriteQueue.BatchSize < 0 {
		errs = append(errs, fmt.Errorf("write_queue.batch_size must be >= 0, got %d", c.WriteQueue.BatchSize))
	}
	if c.WriteQueue.BatchSize > c.WriteQueue.Capacity && c.WriteQueue.Capacity >= 0 && c.WriteQueue.BatchSize >= 0 {
		errs = append(errs, fmt.Errorf("write_queue.batch_size (%d) must be <= capacity (%d)",
			c.WriteQueue.BatchSize, c.WriteQueue.Capacity))
	}
	if c.WriteQueue.Timeout.Duration < 0 {
		errs = append(errs, fmt.Errorf("write_queue.timeout must be >= 0, got %v", c.WriteQueue.Timeout.Duration))
	}

	// Election backend.
	switch c.Election.Backend {
	case "noop", "filelock", "":
		// valid
	default:
		errs = append(errs, fmt.Errorf("election.backend must be \"noop\", \"filelock\", or \"\", got %q",
			c.Election.Backend))
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrConfigValidation, errors.Join(errs...))
}

// validateTLSPair ensures cert and key are both set or both empty.
func validateTLSPair(prefix string, tls *TLSConfig) error {
	hasCert := tls.Cert != ""
	hasKey := tls.Key != ""
	if hasCert != hasKey {
		return fmt.Errorf("%s: cert and key must both be set or both be empty", prefix)
	}
	return nil
}

// checkFileExists appends an error if path is non-empty and the file does not exist.
func checkFileExists(errs *[]error, field, path string) {
	if path == "" {
		return
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		*errs = append(*errs, fmt.Errorf("%s: file not found: %s", field, path))
	}
}
