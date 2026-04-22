package config

import "time"

// DefaultConfig returns a WireConfig populated with default values.
// These defaults match the current pflag defaults in cmd/init.go.
func DefaultConfig() WireConfig {
	return WireConfig{
		Mode:   "coordinator",
		Listen: ":4002",
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
			Backend:  "noop",
			LockPath: "data/coordinator/leader.lock",
		},
		Worker: WorkerConfig{
			ListenAddr: ":4003",
			TaskSlots:  4,
		},
	}
}
