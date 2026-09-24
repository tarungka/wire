package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

type haOutputSink struct{ written *atomic.Uint64 }

func (*haOutputSink) Open(context.Context) error                  { return nil }
func (*haOutputSink) Close() error                                { return nil }
func (s *haOutputSink) Write(context.Context, engine.Event) error { s.written.Add(1); return nil }
func (*haOutputSink) Checkpoint(uint64) ([]byte, error)           { return []byte("sink"), nil }
func (*haOutputSink) RestoreCheckpoint(data []byte) error {
	if string(data) != "sink" {
		return errors.New("bad sink state")
	}
	return nil
}

func TestHAJobRestoresThroughDiscoveryAtDifferentAddress(t *testing.T) {
	testHAJobRestoration(t, false)
}
func TestHAJobRestoresThroughKubernetesLeaseDiscovery(t *testing.T) { testHAJobRestoration(t, true) }

func testHAJobRestoration(t *testing.T, kubernetes bool) {
	t.Helper()
	var leaseServer *httptest.Server
	if kubernetes {
		leaseServer = haLeaseAPI(t)
		defer leaseServer.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	root := t.TempDir()
	open := func() (coordinator.MetadataStore, error) {
		return coordinator.NewPebbleStore(filepath.Join(root, "metadata"))
	}
	services := make([]*coordinator.HAService, 2)
	stops := make([]context.CancelFunc, 2)
	ended := make([]chan error, 2)
	for i := range services {
		var election coordinator.LeaderElection = coordinator.NewFileLockElection(filepath.Join(root, "leader.lock"), "pending")
		if kubernetes {
			backend, err := coordinator.NewKubernetesLeaseElection(coordinator.KubernetesLeaseConfig{APIServer: leaseServer.URL, Namespace: "wire", LeaseName: "coordinator", HTTPClient: leaseServer.Client(), LeaseDuration: 5 * time.Second, RenewDeadline: 3 * time.Second, RetryPeriod: 250 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			election = backend
		}
		services[i] = coordinator.NewHAService(coordinator.CoordinatorConfig{NodeID: fmt.Sprintf("coordinator-%d", i), ListenAddr: "127.0.0.1:0", WorkerTimeout: 5 * time.Second, HeartbeatInterval: 50 * time.Millisecond}, "127.0.0.1:0", election, open, nil, zerolog.Nop())
		if err := services[i].Listen(); err != nil {
			t.Fatal(err)
		}
		runCtx, stop := context.WithCancel(ctx)
		stops[i] = stop
		ended[i] = make(chan error, 1)
		go func(i int) { ended[i] <- services[i].Run(runCtx) }(i)
		if i == 0 {
			waitFor(t, 3*time.Second, func() bool { _, ready := services[0].CurrentCoordinator(); return ready })
		}
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
		for _, done := range ended {
			if done != nil {
				if err := <-done; err != nil {
					t.Error(err)
				}
			}
		}
	}()
	first, _ := services[0].CurrentCoordinator()
	oldEpoch := first.CurrentEpoch()
	if services[0].RPCAddr() == services[1].RPCAddr() {
		t.Fatal("test reused coordinator address")
	}
	var instances atomic.Int32
	var failSource, restored atomic.Bool
	var written atomic.Uint64
	registry := worker.NewRegistry()
	registry.RegisterSource("ha-source", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
		return &restartCheckpointSource{fail: &failSource, needsRestore: instances.Add(1) > 1, readAfterRestore: &restored}, nil
	})
	registry.RegisterSink("ha-sink", func(context.Context, []byte, worker.TaskContext) (engine.SinkOperator, error) {
		return &haOutputSink{written: &written}, nil
	})
	workerDone := make([]chan error, 2)
	for i := range workerDone {
		cfg := worker.Config{WorkerID: fmt.Sprintf("ha-worker-%d", i), CoordinatorSeeds: []string{services[0].HTTPAddr(), services[1].HTTPAddr()}, EpochPath: filepath.Join(t.TempDir(), "epoch"), TaskSlots: 1, HeartbeatInterval: 50 * time.Millisecond, HeartbeatTimeout: 5 * time.Second, CheckpointReplica: &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 1}}
		w := worker.NewWithRegistry(cfg, registry, zerolog.Nop())
		workerDone[i] = make(chan error, 1)
		go func(i int) { workerDone[i] <- w.Run(ctx) }(i)
	}
	defer func() {
		cancel()
		for _, done := range workerDone {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	waitFor(t, 4*time.Second, func() bool { return len(first.ListWorkers()) == 2 })
	graph, err := protocol.EncodeMsgPack(rpc.JobGraph{Operators: []rpc.OperatorDescriptor{{OperatorID: "source", ClassName: "ha-source", Type: rpc.OperatorTypeSource}, {OperatorID: "sink", ClassName: "ha-sink", Type: rpc.OperatorTypeSink}}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "sink"}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := first.SubmitJob("ha-running-job", 1, graph)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		current, err := first.GetJob(job.ID)
		return err == nil && current.Status == coordinator.JobRunning && written.Load() > 0
	})
	checkpoint, err := first.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		current, err := first.GetJob(job.ID)
		return err == nil && current.LatestCheckpoint == checkpoint.ID
	})
	started := time.Now()
	stops[0]()
	if err := <-ended[0]; err != nil {
		t.Fatal(err)
	}
	ended[0] = nil
	waitFor(t, 5*time.Second, func() bool { _, ready := services[1].CurrentCoordinator(); return ready })
	replacement, _ := services[1].CurrentCoordinator()
	if replacement.CurrentEpoch() <= oldEpoch {
		t.Fatal("takeover did not advance durable epoch")
	}
	waitFor(t, 10*time.Second, func() bool {
		current, err := replacement.GetJob(job.ID)
		return err == nil && current.Status == coordinator.JobRunning && current.RestartCount == 1 && current.LatestCheckpoint == checkpoint.ID && restored.Load()
	})
	before := written.Load()
	waitFor(t, time.Second, func() bool { return written.Load() > before })
	if elapsed := time.Since(started); elapsed >= 15*time.Second {
		t.Fatalf("failover exceeded WIP target: %s", elapsed)
	}
	t.Logf("running job restored at a different RPC address in %s", time.Since(started))
	if _, err := first.SubmitJob("stale-term", 1, graph); !errors.Is(err, coordinator.ErrNotLeader) {
		t.Fatalf("old coordinator still accepted work: %v", err)
	}
}

// This HTTPS fixture provides Kubernetes' resource-version compare-and-swap
// behavior. The coordinators, durable metadata, discovery, RPCs and workers
// all run through their production paths; no real cluster/storage driver is
// implied by this test.
func haLeaseAPI(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var record map[string]any
	version := 0
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if record == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		case http.MethodPost, http.MethodPut:
			var next map[string]any
			if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			metadata, ok := next["metadata"].(map[string]any)
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.Method == http.MethodPost && record != nil || r.Method == http.MethodPut && (record == nil || metadata["resourceVersion"] != record["metadata"].(map[string]any)["resourceVersion"]) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			version++
			metadata["resourceVersion"] = strconv.Itoa(version)
			record = next
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(record)
	}))
}
