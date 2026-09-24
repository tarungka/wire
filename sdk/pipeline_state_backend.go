package sdk

// pipelineStateBackend follows WIP-18's nested pipeline configuration. Pointers
// distinguish an omitted HashMap limit (256 MiB) from explicit zero (unlimited).
type pipelineStateBackend struct {
	Type    string `yaml:"type"`
	HashMap struct {
		MaxMemoryMB *int `yaml:"max_memory_mb"`
	} `yaml:"hashmap"`
	Pebble struct {
		DataDir                  string `yaml:"data_dir"`
		MaxCompactionConcurrency int    `yaml:"max_compaction_concurrency"`
	} `yaml:"pebble"`
}

func (c *pipelineStateBackend) compile() (StateBackendConfig, error) {
	cfg := StateBackendConfig{Type: c.Type, MaxMemoryMB: 256, DataDir: c.Pebble.DataDir, MaxCompactionConcurrency: c.Pebble.MaxCompactionConcurrency}
	if cfg.Type == "" {
		cfg.Type = "pebble"
	}
	if c.HashMap.MaxMemoryMB != nil {
		cfg.MaxMemoryMB = *c.HashMap.MaxMemoryMB
	}
	return cfg, cfg.validate()
}

// SetStateBackend overrides the YAML selection using the Go SDK's highest
// precedence. As with the environment setter, Execute validates this choice.
func (p *YAMLPipeline) SetStateBackend(config StateBackendConfig) *YAMLPipeline {
	p.env.SetStateBackend(config)
	return p
}
