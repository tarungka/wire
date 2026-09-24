package sdk

import (
	"context"
	"fmt"
	"sync"
)

// MiniClusterConfig configures a MiniCluster for integration testing.
type MiniClusterConfig struct {
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
	joined sync.WaitGroup
}

// NewMiniCluster creates a new MiniCluster with the given configuration.
func NewMiniCluster(config MiniClusterConfig) *MiniCluster {
	if config.NumTaskSlots <= 0 {
		config.NumTaskSlots = 1
	}
	return &MiniCluster{config: config, active: make(map[*StreamExecutionEnvironment]context.CancelFunc)}
}

// GetExecutionEnvironment returns a pre-configured StreamExecutionEnvironment
// for running pipelines on this MiniCluster.
func (mc *MiniCluster) GetExecutionEnvironment() *StreamExecutionEnvironment {
	env := New()
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
