package config

// WireConfig is the top-level configuration for a Wire node.
// It maps directly to the wire.yaml schema.
type WireConfig struct {
	Mode          string              `yaml:"mode"        json:"mode"        koanf:"mode"`
	Listen        string              `yaml:"listen"      json:"listen"      koanf:"listen"`
	Node          NodeConfig          `yaml:"node"        json:"node"        koanf:"node"`
	HTTP          HTTPConfig          `yaml:"http"        json:"http"        koanf:"http"`
	NodeTLS       TLSConfig           `yaml:"node_tls"    json:"node_tls"    koanf:"node_tls"`
	Auth          AuthConfig          `yaml:"auth"        json:"auth"        koanf:"auth"`
	WriteQueue    WriteQueueConfig    `yaml:"write_queue" json:"write_queue" koanf:"write_queue"`
	Election      ElectionConfig      `yaml:"election"      json:"election"      koanf:"election"`
	Worker        WorkerConfig        `yaml:"worker"        json:"worker"        koanf:"worker"`
	Observability ObservabilityConfig `yaml:"observability" json:"observability" koanf:"observability"`
}

// ObservabilityConfig holds metrics and tracing export settings.
type ObservabilityConfig struct {
	Enabled     bool   `yaml:"enabled"      json:"enabled"      koanf:"enabled"`
	MetricsAddr string `yaml:"metrics_addr" json:"metrics_addr" koanf:"metrics_addr"`
}

// WorkerConfig holds settings for running in worker mode.
type WorkerConfig struct {
	CoordinatorAddr string `yaml:"coordinator_addr" json:"coordinator_addr" koanf:"coordinator_addr"`
	WorkerID        string `yaml:"worker_id"        json:"worker_id"        koanf:"worker_id"`
	ListenAddr      string `yaml:"listen_addr"      json:"listen_addr"      koanf:"listen_addr"`
	TaskSlots       int    `yaml:"task_slots"       json:"task_slots"       koanf:"task_slots"`
}

// NodeConfig holds node identity and storage settings.
type NodeConfig struct {
	ID      string `yaml:"id"       json:"id"       koanf:"id"`
	DataDir string `yaml:"data_dir" json:"data_dir" koanf:"data_dir"`
	StoreDB string `yaml:"store_db" json:"store_db" koanf:"store_db"`
	Debug   bool   `yaml:"debug"    json:"debug"    koanf:"debug"`
}

// HTTPConfig holds HTTP API server settings.
type HTTPConfig struct {
	Addr        string    `yaml:"addr"         json:"addr"         koanf:"addr"`
	AdvAddr     string    `yaml:"adv_addr"     json:"adv_addr"     koanf:"adv_addr"`
	AllowOrigin string    `yaml:"allow_origin" json:"allow_origin" koanf:"allow_origin"`
	TLS         TLSConfig `yaml:"tls"          json:"tls"          koanf:"tls"`
}

// TLSConfig holds TLS certificate paths and verification settings.
type TLSConfig struct {
	Cert             string `yaml:"cert"               json:"cert"               koanf:"cert"`
	Key              string `yaml:"key"                json:"key"                koanf:"key"`
	CACert           string `yaml:"ca_cert"            json:"ca_cert"            koanf:"ca_cert"`
	VerifyClient     bool   `yaml:"verify_client"      json:"verify_client"      koanf:"verify_client"`
	VerifyServerName string `yaml:"verify_server_name" json:"verify_server_name" koanf:"verify_server_name"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	File string `yaml:"file" json:"file" koanf:"file"`
}

// WriteQueueConfig holds internal write queue tuning parameters.
type WriteQueueConfig struct {
	Capacity      int      `yaml:"capacity"      json:"capacity"      koanf:"capacity"`
	BatchSize     int      `yaml:"batch_size"    json:"batch_size"    koanf:"batch_size"`
	Timeout       Duration `yaml:"timeout"       json:"timeout"       koanf:"timeout"`
	Transactional bool     `yaml:"transactional" json:"transactional" koanf:"transactional"`
}

// ElectionConfig holds leader election settings.
type ElectionConfig struct {
	Backend  string `yaml:"backend"   json:"backend"   koanf:"backend"`
	LockPath string `yaml:"lock_path" json:"lock_path" koanf:"lock_path"`
}
