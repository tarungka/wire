package coordinator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestHTTP_ClusterStatus(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	// Add a worker to the cache.
	if err := c.persistWorker(&WorkerMeta{
		ID:                 "w1",
		Address:            "worker1:5001",
		TaskSlotsTotal:     4,
		TaskSlotsAvailable: 3,
		LastHeartbeat:      time.Now().UTC(),
		RunningTasks:       []string{"t1"},
	}); err != nil {
		t.Fatalf("persistWorker: %v", err)
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/cluster", srv.Addr()))
	if err != nil {
		t.Fatalf("GET /api/v1/cluster: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result clusterStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if result.Leader == nil {
		t.Fatal("expected leader info")
	}
	if len(result.Workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(result.Workers))
	}
	if result.Workers[0].ID != "w1" {
		t.Fatalf("expected worker w1, got %s", result.Workers[0].ID)
	}
}

func TestHTTP_RemoveNode(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	if err := c.persistWorker(&WorkerMeta{
		ID:      "w1",
		Address: "worker1:5001",
	}); err != nil {
		t.Fatalf("persistWorker: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://%s/api/v1/cluster/nodes/w1", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify worker is gone.
	c.mu.RLock()
	_, ok := c.workers["w1"]
	c.mu.RUnlock()
	if ok {
		t.Fatal("expected worker w1 to be removed")
	}
}

func TestHTTP_RemoveNode_NotFound(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	srv := startTestHTTPServer(t, c)

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://%s/api/v1/cluster/nodes/nonexistent", srv.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
