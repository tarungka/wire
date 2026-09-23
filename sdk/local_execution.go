package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/worker"
)

// runLocal uses production scheduling, streams, replicas and recovery. The
// coordinator metadata and replica archives live for this Execute invocation.
func (env *StreamExecutionEnvironment) runLocal(ctx context.Context, name string, slots int) (*JobResult, error) {
	start := time.Now()
	graph, registry, closeRegistry, err := env.localRegistry(ctx)
	if err != nil {
		return nil, err
	}
	defer closeRegistry()
	root, err := os.MkdirTemp("", "wire-mini-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	store := coordinator.NewMemoryStore()
	defer store.Close()
	log := zerolog.Nop()
	coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "mini-coordinator", WorkerTimeout: 5 * time.Second}, store, nil, log)
	var joined sync.WaitGroup
	failures := make(chan error, 1)
	launch := func(run func() error) {
		joined.Add(1)
		go func() {
			defer joined.Done()
			if err := run(); err != nil && runCtx.Err() == nil {
				select {
				case failures <- err:
				default:
				}
				cancel()
			}
		}()
	}
	var taskErrorsMu sync.Mutex
	var taskErrors []error
	var workers []*worker.Worker
	var transport *coordinator.TransportServer
	defer func() {
		cancel()
		for _, w := range workers {
			_ = w.Shutdown(context.Background())
		}
		if transport != nil {
			_ = transport.Shutdown(context.Background())
		}
		joined.Wait()
	}()
	launch(func() error { return coord.Run(runCtx) })
	wait := func(ready func() bool) error {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for !ready() {
			select {
			case err := <-failures:
				return err
			case <-runCtx.Done():
				return runCtx.Err()
			case <-ticker.C:
			}
		}
		return nil
	}
	if err := wait(coord.IsReady); err != nil {
		return nil, err
	}
	transport = coordinator.NewTransportServer(coord, "127.0.0.1:0", log)
	if err := transport.Listen(); err != nil {
		return nil, err
	}
	launch(func() error { return transport.Serve(runCtx) })
	// Provision enough slots even if no operators can be fused. At least two
	// workers provide independent replica storage for checkpoint recovery.
	total := 0
	for _, op := range graph.Operators {
		total += int(op.Parallelism)
	}
	slots = max(1, slots)
	workerCount := max(2, (total+slots-1)/slots)
	for i := 0; i < workerCount; i++ {
		dir := filepath.Join(root, fmt.Sprint(i))
		for _, subdir := range []string{dir, filepath.Join(dir, "replica"), filepath.Join(dir, "staging")} {
			if err := os.MkdirAll(subdir, 0700); err != nil {
				return nil, err
			}
		}
		w := worker.NewWithRegistry(worker.Config{
			TaskFailureObserver: func(_, _ string, err error) {
				taskErrorsMu.Lock()
				if len(taskErrors) == 32 {
					taskErrors = taskErrors[1:]
				}
				taskErrors = append(taskErrors, err)
				taskErrorsMu.Unlock()
			},
			WorkerID: fmt.Sprintf("mini-worker-%d", i), CoordinatorAddr: transport.Addr(), ListenAddr: "127.0.0.1:0", TaskSlots: slots,
			HeartbeatInterval: 100 * time.Millisecond, HeartbeatTimeout: 5 * time.Second,
			CheckpointReplica: &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: filepath.Join(dir, "replica"), ArtifactRoot: dir, StagingRoot: filepath.Join(dir, "staging"), Concurrency: max(1, total)},
		}, registry, log)
		workers = append(workers, w)
		launch(func() error { return w.Run(runCtx) })
	}
	if err := wait(func() bool { return len(coord.ListWorkers()) == workerCount }); err != nil {
		return nil, err
	}
	data, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = "mini-job"
	}
	job, err := coord.SubmitJob(name, env.parallelism, data)
	if err != nil {
		return nil, err
	}
	result := &JobResult{JobID: job.ID}
	err = wait(func() bool {
		latest, getErr := coord.GetJob(job.ID)
		if getErr != nil {
			return false
		}
		job = latest
		return job.Status.IsTerminal()
	})
	if err != nil || job.Status != coordinator.JobFinished {
		taskErrorsMu.Lock()
		err = errors.Join(append([]error{err, fmt.Errorf("sdk: job %s ended with status %s: %s", job.ID, job.Status, job.CheckpointFailure)}, taskErrors...)...)
		taskErrorsMu.Unlock()
	}
	result.Err = err
	result.Metrics.Duration = time.Since(start)
	return result, err
}
