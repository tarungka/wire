package config

import "time"

// DefaultConfig returns a WireConfig populated with default values.
// These defaults match the current pflag defaults in cmd/init.go.
func DefaultConfig() WireConfig {
	return WireConfig{
		Heartbeat:  HeartbeatConfig{Interval: Duration{5 * time.Second}, Timeout: Duration{30 * time.Second}},
		Checkpoint: CheckpointConfig{Timeout: Duration{10 * time.Minute}},
		TaskSlot:   TaskSlotConfig{InputBufferSize: 1024, OutputBufferSize: 1024, AlignmentBufferSize: 4096, CheckpointUploadConcurrency: 1, DrainTimeout: Duration{5 * time.Second}},
		Mode:       "coordinator",
		Listen:     ":4002",
		Node: NodeConfig{
			DataDir: "data/coordinator",
			StoreDB: "pebble",
		},
		HTTP: HTTPConfig{
			Addr: ":4001",
		},
		WriteQueue: WriteQueueConfig{
			Capacity:  1024,
			BatchSize: 128,
			Timeout:   Duration{50 * time.Millisecond},
		},
		Election: ElectionConfig{
			Kubernetes: KubernetesElectionConfig{LeaseName: "wire-coordinator", LeaseDuration: Duration{10 * time.Second}, RenewDeadline: Duration{6 * time.Second}, RetryPeriod: Duration{time.Second}},
			Backend:    "noop",
			LockPath:   "data/coordinator/leader.lock",
		},
		Worker: WorkerConfig{
			EpochPath:         "data/worker/epoch",
			CheckpointReplica: CheckpointReplicaConfig{Concurrency: 1},
			ListenAddr:        ":4003",
			TaskSlots:         4,
		},
	}
}
