package coordinator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/observability"
)

// The provider and Prometheus registry are process-global. Exercise production
// Init in a subprocess so this acceptance check cannot change other tests.
func TestMetricsListenerRemainsPublicWithAPIAuthentication(t *testing.T) {
	const child = "WIRE_TEST_PUBLIC_METRICS"
	if os.Getenv(child) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMetricsListenerRemainsPublicWithAPIAuthentication$")
		cmd.Env = append(os.Environ(), child+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("metrics acceptance: %v\n%s", err, output)
		}
		return
	}
	addresses := make(chan string, 1)
	log := zerolog.New(metricsAddressWriter{addresses})
	shutdown, err := observability.Init(context.Background(), observability.Config{Enabled: true, MetricsAddr: "127.0.0.1:0", ServiceName: "security-acceptance"}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	var address string
	select {
	case address = <-addresses:
	case <-time.After(time.Second):
		t.Fatal("metrics listener did not publish address")
	}
	c, _ := newTestCoordinator(t)
	server := NewHTTPServer(c, "", zerolog.Nop(), nil)
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"users":[{"username":"viewer","role":"viewer","api_key":"wk_live_`+strings.Repeat("a", 32)+`"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.ConfigureAuth(path); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewTLSServer(server.server.Handler)
	defer api.Close()
	response, err := api.Client().Get(api.URL + "/api/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("API without credentials: %d", response.StatusCode)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	for _, authorization := range []string{"", "Bearer invalid"} {
		req, err := http.NewRequest(http.MethodGet, "http://"+address+"/metrics", nil)
		if err != nil {
			t.Fatal(err)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "go_goroutines") {
			t.Fatalf("public scrape failed: status=%d", response.StatusCode)
		}
		if response.TLS != nil {
			t.Fatal("metrics unexpectedly shares API TLS")
		}
	}
}

type metricsAddressWriter struct{ addresses chan<- string }

func (w metricsAddressWriter) Write(data []byte) (int, error) {
	var entry struct {
		Address string `json:"addr"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &entry) == nil && entry.Message == "metrics endpoint listening" {
		select {
		case w.addresses <- entry.Address:
		default:
		}
	}
	return len(data), nil
}
