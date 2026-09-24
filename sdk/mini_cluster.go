package sdk

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MiniClusterConfig configures a MiniCluster for integration testing.
type MiniClusterConfig struct {
	// NumWorkers is a minimum worker count, allowing spare capacity for rescale
	// tests. Zero provisions only the workers needed by the initial graph.
	NumWorkers int
	// NumTaskSlots sets each local worker's capacity and the environment's
	// default parallelism. Workers are provisioned to fit the submitted graph,
	// with at least two workers for independent checkpoint replicas.
	NumTaskSlots int
}

// MiniCluster is a lightweight, in-process cluster for integration testing.
// Each execution runs a local coordinator and workers with checkpoint replicas.
type MiniCluster struct {
	config MiniClusterConfig
	mu     sync.Mutex
	closed bool
	active map[*StreamExecutionEnvironment]context.CancelFunc
	jobs   map[string]MiniClusterJob
	joined sync.WaitGroup
}

// NewMiniCluster creates a new MiniCluster with the given configuration.
func NewMiniCluster(config MiniClusterConfig) *MiniCluster {
	if config.NumTaskSlots <= 0 {
		config.NumTaskSlots = 1
	}
	return &MiniCluster{config: config, active: make(map[*StreamExecutionEnvironment]context.CancelFunc), jobs: make(map[string]MiniClusterJob)}
}

// GetExecutionEnvironment returns a pre-configured StreamExecutionEnvironment
// for running pipelines on this MiniCluster. Managed Process/window state
// defaults to HashMap with a 256 MiB logical payload limit per instance. Call
// SetStateBackend on the returned environment to select another backend.
func (mc *MiniCluster) GetExecutionEnvironment() *StreamExecutionEnvironment {
	env := New().SetStateBackend(NewHashMapStateBackend(256))
	env.SetParallelism(mc.config.NumTaskSlots)
	env.SetMode(Embedded)
	env.miniCluster = mc
	return env
}

// Shutdown cancels active executions and waits for their workers, streams and
// checkpoint resources to close. It is idempotent; later executions fail.
func (mc *MiniCluster) Shutdown() error {
	mc.mu.Lock()
	mc.closed = true
	for _, cancel := range mc.active {
		cancel()
	}
	mc.mu.Unlock()
	mc.joined.Wait()
	return nil
}

func (mc *MiniCluster) run(ctx context.Context, env *StreamExecutionEnvironment, name string) (*JobResult, error) {
	mc.mu.Lock()
	if mc.closed {
		mc.mu.Unlock()
		return nil, fmt.Errorf("sdk: MiniCluster is shut down")
	}
	ctx, cancel := context.WithCancel(ctx)
	mc.active[env] = cancel
	mc.joined.Add(1)
	mc.mu.Unlock()
	defer func() { cancel(); mc.mu.Lock(); delete(mc.active, env); mc.mu.Unlock(); mc.joined.Done() }()
	return env.runLocal(ctx, name, mc.config.NumTaskSlots)
}

// MiniClusterJob identifies a running execution and its loopback HTTP control
// API. The API is unauthenticated and exists only for the Execute invocation.
type MiniClusterJob struct {
	JobID          string
	CoordinatorURL string
}

// Jobs returns a detached, ordered snapshot of active executions. A job may
// finish after this call, so control requests must handle a closed endpoint.
func (mc *MiniCluster) Jobs() []MiniClusterJob {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	jobs := make([]MiniClusterJob, 0, len(mc.jobs))
	for _, job := range mc.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].JobID < jobs[j].JobID })
	return jobs
}

func (mc *MiniCluster) publishJob(job MiniClusterJob) func() {
	mc.mu.Lock()
	mc.jobs[job.JobID] = job
	mc.mu.Unlock()
	return func() { mc.mu.Lock(); delete(mc.jobs, job.JobID); mc.mu.Unlock() }
}
