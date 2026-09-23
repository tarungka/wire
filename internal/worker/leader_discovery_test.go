package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoveryConfirmsLeaderAndSkipsStaleSeeds(t *testing.T) {
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cluster/leader" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(discoveredLeader{RPCAddr: "localhost:4567", Epoch: 12, IsSelf: true, Ready: true})
	}))
	defer leader.Close()
	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(discoveredLeader{HTTPAddr: leader.URL, RPCAddr: "wrong:1111", Epoch: 1})
	}))
	defer standby.Close()
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(discoveredLeader{RPCAddr: "stale:4567", Epoch: 9, IsSelf: true, Ready: true})
	}))
	defer stale.Close()
	worker := &Worker{cfg: Config{CoordinatorSeeds: []string{stale.URL, standby.URL}}, epoch: 10}
	addr, err := worker.discoverCoordinator(context.Background())
	if err != nil || addr != "localhost:4567" {
		t.Fatalf("discovery: %s %v", addr, err)
	}
}

func TestDiscoveryRejectsUnreadyLeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(discoveredLeader{RPCAddr: "localhost:4567", Epoch: 12, IsSelf: true})
	}))
	defer server.Close()
	worker := &Worker{cfg: Config{CoordinatorSeeds: []string{server.URL}}}
	if _, err := worker.discoverCoordinator(context.Background()); err == nil {
		t.Fatal("discovered leader before recovery was ready")
	}
}

func TestDiscoveryCancellationStopsBlockedSeed(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer server.Close()
	worker := &Worker{cfg: Config{CoordinatorSeeds: []string{server.URL}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := worker.discoverCoordinator(ctx); done <- err }()
	<-entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("discovery ignored cancellation")
	}
}
