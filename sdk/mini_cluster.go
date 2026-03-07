package sdk

// MiniClusterConfig configures a MiniCluster for integration testing.
type MiniClusterConfig struct {
	NumTaskSlots int
}

// MiniCluster is a lightweight, in-process cluster for integration testing.
// It wraps the embedded executor with pre-configured settings.
type MiniCluster struct {
	config MiniClusterConfig
}

// NewMiniCluster creates a new MiniCluster with the given configuration.
func NewMiniCluster(config MiniClusterConfig) *MiniCluster {
	if config.NumTaskSlots <= 0 {
		config.NumTaskSlots = 1
	}
	return &MiniCluster{config: config}
}

// GetExecutionEnvironment returns a pre-configured StreamExecutionEnvironment
// for running pipelines on this MiniCluster.
func (mc *MiniCluster) GetExecutionEnvironment() *StreamExecutionEnvironment {
	env := New()
	env.SetParallelism(mc.config.NumTaskSlots)
	env.SetMode(Embedded)
	return env
}

// Shutdown releases resources held by the MiniCluster.
func (mc *MiniCluster) Shutdown() error {
	// No resources to release in embedded mode.
	return nil
}
