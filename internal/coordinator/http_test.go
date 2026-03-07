package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func startTestHTTPServer(t *testing.T, c *Coordinator) *HTTPServer {
	t.Helper()
	srv := NewHTTPServer(c, "127.0.0.1:0", zerolog.Nop())

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()

	t.Cleanup(func() {
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Logf("srv.Shutdown: %v", err)
		}
		<-done
	})
	return srv
}

func TestHTTP_Healthz(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	srv := startTestHTTPServer(t, c)

	resp, err := http.Get(fmt.Sprintf("http://%s/healthz", srv.Addr()))
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHTTP_Readyz_Standby(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Coordinator in STANDBY (not ready), no leader info available.
	// Without a known leader address, falls back to 503.
	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	srv := startTestHTTPServer(t, c)

	// Use a client that doesn't follow redirects.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(fmt.Sprintf("http://%s/readyz", srv.Addr()))
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Single-node with no leader address → 503 (no redirect target).
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for standby without leader, got %d", resp.StatusCode)
	}
}

func TestHTTP_Readyz_StandbyRedirect(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Use a NoopElection so GetLeaderInfo returns a known leader address.
	election := NewNoopElection("leader-host:4001")
	if _, err := election.Campaign(context.Background(), "leader-node"); err != nil {
		t.Fatalf("Campaign: %v", err)
	}

	c := New(CoordinatorConfig{
		NodeID:     "standby-node",
		ListenAddr: ":4002",
	}, store, election, zerolog.Nop())
	// Don't run the coordinator — leave it in STANDBY so IsReady() returns false.

	srv := startTestHTTPServer(t, c)

	// Use a client that doesn't follow redirects.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(fmt.Sprintf("http://%s/readyz", srv.Addr()))
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 for standby with leader, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatal("expected Location header in 307 response")
	}
	if loc != "http://leader-host:4001/readyz" {
		t.Fatalf("expected Location http://leader-host:4001/readyz, got %s", loc)
	}

	leaderID := resp.Header.Get("X-Wire-Leader-Id")
	if leaderID != "leader-node" {
		t.Fatalf("expected X-Wire-Leader-Id leader-node, got %s", leaderID)
	}
}

func TestHTTP_Readyz_Leader(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		HeartbeatFlushInterval: time.Hour, // don't flush in test
	}, store, nil, zerolog.Nop())

	go func() { _ = c.Run(t.Context()) }()
	time.Sleep(100 * time.Millisecond)

	srv := startTestHTTPServer(t, c)

	resp, err := http.Get(fmt.Sprintf("http://%s/readyz", srv.Addr()))
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for leader, got %d", resp.StatusCode)
	}
}

func TestHTTP_LeaderInfo(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		ListenAddr:             ":4001",
		HeartbeatFlushInterval: time.Hour,
	}, store, nil, zerolog.Nop())

	go func() { _ = c.Run(t.Context()) }()
	time.Sleep(100 * time.Millisecond)

	srv := startTestHTTPServer(t, c)

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/cluster/leader", srv.Addr()))
	if err != nil {
		t.Fatalf("GET /api/v1/cluster/leader: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result leaderResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.LeaderID != "n1" {
		t.Fatalf("expected leader_id n1, got %s", result.LeaderID)
	}
	if result.LeaderHTTPAddr != ":4001" {
		t.Fatalf("expected addr :4001, got %s", result.LeaderHTTPAddr)
	}
	if !result.IsSelf {
		t.Fatal("expected is_self=true")
	}
	if result.LeaderEpoch == 0 {
		t.Fatal("expected non-zero epoch")
	}
}

func TestHTTP_LeaderInfo_NoLeader(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	// Coordinator in STANDBY (no leader).
	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	srv := startTestHTTPServer(t, c)

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/cluster/leader", srv.Addr()))
	if err != nil {
		t.Fatalf("GET /api/v1/cluster/leader: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Single-node mode with no Run() → still returns info (epoch=0).
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
