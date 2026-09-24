// Package apiclient provides authenticated coordinator HTTP clients.
package apiclient

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tarungka/wire/internal/transport"
)

type Config struct {
	CACert, ClientCert, ClientKey      string
	APIKeyFile, Username, PasswordFile string
}

// Client is bound to one coordinator origin. It never follows redirects or
// forwards credentials to another origin. Construct a new client for takeover.
type Client struct {
	http                      *http.Client
	origin                    string
	token, username, password string
}

func New(endpoint string, cfg Config, timeout time.Duration) (*Client, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("invalid coordinator URL")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("API client timeout must be positive")
	}
	if (cfg.Username == "") != (cfg.PasswordFile == "") {
		return nil, fmt.Errorf("username and password file must be supplied together")
	}
	if cfg.APIKeyFile != "" && cfg.Username != "" {
		return nil, fmt.Errorf("choose API key or username/password authentication")
	}
	secured := cfg.APIKeyFile != "" || cfg.Username != "" || cfg.CACert != "" || cfg.ClientCert != "" || cfg.ClientKey != ""
	if secured && base.Scheme != "https" {
		return nil, fmt.Errorf("credentials and TLS options require an HTTPS coordinator URL")
	}
	c := &Client{origin: base.Scheme + "://" + base.Host, username: cfg.Username}
	if cfg.APIKeyFile != "" {
		c.token, err = readCredential(cfg.APIKeyFile)
		if err != nil {
			return nil, err
		}
	}
	if c.token != "" {
		for _, char := range c.token {
			if char < 33 || char > 126 {
				return nil, fmt.Errorf("API key contains invalid characters")
			}
		}
	}
	if strings.ContainsAny(cfg.Username, ":\r\n") {
		return nil, fmt.Errorf("invalid Basic authentication username")
	}
	if cfg.PasswordFile != "" {
		c.password, err = readCredential(cfg.PasswordFile)
		if err != nil {
			return nil, err
		}
	}
	tlsConfig, err := transport.NewTLSClientConfig(cfg.ClientCert, cfg.ClientKey, cfg.CACert)
	if err != nil {
		return nil, fmt.Errorf("API client TLS: %w", err)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsConfig
	c.http = &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return c, nil
}

func readCredential(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open API credential file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil {
		return "", fmt.Errorf("read API credential file: %w", err)
	}
	if len(data) > 16384 {
		return "", fmt.Errorf("API credential file exceeds 16 KiB")
	}
	// Permit a single text-file line ending without altering password whitespace.
	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("API credential must be one nonempty line")
	}
	return value, nil
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.User != nil || (req.Host != "" && req.Host != req.URL.Host) || req.URL.Scheme+"://"+req.URL.Host != c.origin {
		return nil, fmt.Errorf("API request origin differs from configured coordinator")
	}
	owned := req.Clone(req.Context())
	if c.token != "" {
		owned.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.username != "" {
		owned.SetBasicAuth(c.username, c.password)
	}
	return c.http.Do(owned)
}
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }
