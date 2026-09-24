package sdk

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/worker"
)

// WorkerConfig configures a worker that executes registered SDK operators.
// CoordinatorAddr is the wire RPC address, not the coordinator HTTP URL.
type WorkerConfig struct {
	WorkerID, CoordinatorAddr, ListenAddr string
	TaskSlots                             int
	HeartbeatInterval, HeartbeatTimeout   time.Duration
	RPCTLSConfig                          *tls.Config
	// CheckpointDirectory enables replica storage. At least two configured
	// workers are needed to checkpoint a job. This directory is retained.
	CheckpointDirectory                     string
	ReplicaListenAddr, ReplicaAdvertiseAddr string
	CheckpointConcurrency                   int
	// ShutdownTimeout bounds joining tasks after Run returns or ctx ends.
	ShutdownTimeout time.Duration
}

// RunWorker registers with a coordinator and runs until cancellation or a
// connection failure. It closes transports and joins tasks before returning.
// Factories may use config bytes in any application-defined encoding.
func RunWorker(ctx context.Context, config WorkerConfig, registry *WorkerRegistry) error {
	if registry == nil || registry.registry == nil {
		return fmt.Errorf("sdk: worker registry is required")
	}
	if config.CoordinatorAddr == "" {
		return fmt.Errorf("sdk: coordinator RPC address is required")
	}
	if config.TaskSlots < 0 || config.CheckpointConcurrency < 0 || config.ShutdownTimeout < 0 || config.HeartbeatInterval < 0 || config.HeartbeatTimeout < 0 {
		return fmt.Errorf("sdk: worker limits and durations must be nonnegative")
	}
	if config.TaskSlots == 0 {
		config.TaskSlots = 4
	}
	if config.ListenAddr == "" {
		config.ListenAddr = "127.0.0.1:0"
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = 30 * time.Second
	}
	cfg := worker.Config{WorkerID: config.WorkerID, CoordinatorAddr: config.CoordinatorAddr, ListenAddr: config.ListenAddr, TaskSlots: config.TaskSlots, HeartbeatInterval: config.HeartbeatInterval, HeartbeatTimeout: config.HeartbeatTimeout}
	if config.RPCTLSConfig != nil {
		cfg.RPCTLSConfig = config.RPCTLSConfig.Clone()
	}
	if config.CheckpointDirectory != "" {
		if config.ReplicaListenAddr == "" {
			config.ReplicaListenAddr = "127.0.0.1:0"
		}
		if config.CheckpointConcurrency == 0 {
			config.CheckpointConcurrency = 8
		}
		root := config.CheckpointDirectory
		for _, subdir := range []string{"replicas", "artifacts", "staging"} {
			if err := os.MkdirAll(filepath.Join(root, subdir), 0700); err != nil {
				return err
			}
		}
		cfg.CheckpointReplica = &worker.CheckpointReplicaConfig{ListenAddr: config.ReplicaListenAddr, AdvertiseAddr: config.ReplicaAdvertiseAddr, StoreRoot: filepath.Join(root, "replicas"), ArtifactRoot: filepath.Join(root, "artifacts"), StagingRoot: filepath.Join(root, "staging"), Concurrency: config.CheckpointConcurrency}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := worker.NewWithRegistry(cfg, registry.registry, zerolog.Nop())
	err := w.Run(runCtx)
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer shutdownCancel()
	return errors.Join(err, w.Shutdown(shutdownCtx))
}
