package worker

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tarungka/wire/internal/apiclient"
)

func TestDiscoveryHTTPSCredentialsAndSeedBoundary(t *testing.T) {
	for _, scenario := range []string{"trusted", "unlisted", "downgrade", "bare-hint"} {
		t.Run(scenario, func(t *testing.T) {
			var leaderCalls atomic.Int32
			leader := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				leaderCalls.Add(1)
				if r.Header.Get("Authorization") != "Bearer discovery-key" {
					t.Error("missing leader credential")
					w.WriteHeader(401)
					return
				}
				_ = json.NewEncoder(w).Encode(discoveredLeader{RPCAddr: "localhost:4567", Epoch: 12, IsSelf: true, Ready: true})
			}))
			defer leader.Close()
			hint := leader.URL
			if scenario == "downgrade" {
				hint = strings.Replace(hint, "https:", "http:", 1)
			}
			if scenario == "bare-hint" {
				hint = strings.TrimPrefix(hint, "https://")
			}
			standby := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer discovery-key" {
					t.Error("missing standby credential")
					w.WriteHeader(401)
					return
				}
				_ = json.NewEncoder(w).Encode(discoveredLeader{HTTPAddr: hint})
			}))
			defer standby.Close()
			dir := t.TempDir()
			ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "key")
			roots := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leader.Certificate().Raw}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: standby.Certificate().Raw})...)
			if err := os.WriteFile(ca, roots, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(key, []byte("discovery-key\n"), 0600); err != nil {
				t.Fatal(err)
			}
			seeds := []string{standby.URL}
			if scenario == "trusted" || scenario == "bare-hint" {
				seeds = append(seeds, leader.URL)
			}
			w := &Worker{cfg: Config{CoordinatorSeeds: seeds, DiscoverySecurity: apiclient.Config{CACert: ca, APIKeyFile: key}}, epoch: 10}
			address, err := w.discoverCoordinator(t.Context())
			if scenario == "trusted" || scenario == "bare-hint" {
				if err != nil || address != "localhost:4567" || leaderCalls.Load() != 1 {
					t.Fatalf("address=%s calls=%d err=%v", address, leaderCalls.Load(), err)
				}
			} else if err == nil || leaderCalls.Load() != 0 {
				t.Fatalf("unsafe hint followed: calls=%d err=%v", leaderCalls.Load(), err)
			}
		})
	}
}
func TestDiscoveryRefusesCredentialsOnPlaintextSeed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	w := &Worker{cfg: Config{CoordinatorSeeds: []string{server.URL}, DiscoverySecurity: apiclient.Config{APIKeyFile: "never-read"}}}
	if _, err := w.discoverCoordinator(t.Context()); err == nil {
		t.Fatal("allowed plaintext discovery credentials")
	}
	if calls.Load() != 0 {
		t.Fatal("sent plaintext request")
	}
}
