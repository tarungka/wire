package sdk

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
)

func TestPublicWorkerUsesAuthenticatedHTTPSDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	store := coordinator.NewMemoryStore()
	defer store.Close()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "secure-discovery"}, store, nil, zerolog.Nop())
	rpc := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
	if err := rpc.Listen(); err != nil {
		t.Fatal(err)
	}
	var joined sync.WaitGroup
	start := func(fn func()) { joined.Add(1); go func() { defer joined.Done(); fn() }() }
	defer func() {
		cancel()
		shutdown, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = rpc.Shutdown(shutdown)
		joined.Wait()
	}()
	start(func() { _ = coord.Run(ctx) })
	start(func() { _ = rpc.Serve(ctx) })
	wait := func(ready func() bool) {
		t.Helper()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatal("worker discovery timeout")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	wait(coord.IsReady)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer worker-key" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"leader_rpc_addr": rpc.Addr(), "leader_epoch": coord.CurrentEpoch(), "is_self": true, "ready": true})
	}))
	defer server.Close()
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "key")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("worker-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	start(func() {
		done <- RunWorker(ctx, WorkerConfig{WorkerID: "secured-worker", CoordinatorSeeds: []string{server.URL}, EpochPath: filepath.Join(dir, "epoch"), DiscoverySecurity: CoordinatorSecurity{CACert: ca, APIKeyFile: key}}, NewWorkerRegistry())
	})
	wait(func() bool { return len(coord.ListWorkers()) == 1 })
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
