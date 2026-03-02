package config

// WireConfig is the top-level configuration for a Wire node.
// It maps directly to the wire.yaml schema.
type WireConfig struct {
	Node       NodeConfig       `yaml:"node"        json:"node"`
	HTTP       HTTPConfig       `yaml:"http"        json:"http"`
	NodeTLS    TLSConfig        `yaml:"node_tls"    json:"node_tls"`
	Auth       AuthConfig       `yaml:"auth"        json:"auth"`
	WriteQueue WriteQueueConfig `yaml:"write_queue" json:"write_queue"`
	Election   ElectionConfig   `yaml:"election"    json:"election"`
}

// NodeConfig holds node identity and storage settings.
type NodeConfig struct {
	ID      string `yaml:"id"       json:"id"`
	DataDir string `yaml:"data_dir" json:"data_dir"`
	StoreDB string `yaml:"store_db" json:"store_db"`
	Debug   bool   `yaml:"debug"    json:"debug"`
}

// HTTPConfig holds HTTP API server settings.
type HTTPConfig struct {
	Addr        string    `yaml:"addr"         json:"addr"`
	AdvAddr     string    `yaml:"adv_addr"     json:"adv_addr"`
	AllowOrigin string    `yaml:"allow_origin" json:"allow_origin"`
	TLS         TLSConfig `yaml:"tls"          json:"tls"`
}

// TLSConfig holds TLS certificate paths and verification settings.
type TLSConfig struct {
	Cert             string `yaml:"cert"               json:"cert"`
	Key              string `yaml:"key"                json:"key"`
	CACert           string `yaml:"ca_cert"            json:"ca_cert"`
	VerifyClient     bool   `yaml:"verify_client"      json:"verify_client"`
	VerifyServerName string `yaml:"verify_server_name" json:"verify_server_name"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	File string `yaml:"file" json:"file"`
}

// WriteQueueConfig holds internal write queue tuning parameters.
type WriteQueueConfig struct {
	Capacity      int      `yaml:"capacity"      json:"capacity"`
	BatchSize     int      `yaml:"batch_size"    json:"batch_size"`
	Timeout       Duration `yaml:"timeout"       json:"timeout"`
	Transactional bool     `yaml:"transactional" json:"transactional"`
}

// ElectionConfig holds leader election settings.
type ElectionConfig struct {
	Backend  string `yaml:"backend"   json:"backend"`
	LockPath string `yaml:"lock_path" json:"lock_path"`
}
