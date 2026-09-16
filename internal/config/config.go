package config

// WireConfig is the top-level configuration for a Wire node.
// It maps directly to the wire.yaml schema.
type WireConfig struct {
	Heartbeat  HeartbeatConfig  `yaml:"heartbeat" json:"heartbeat" koanf:"heartbeat"`
	Checkpoint CheckpointConfig `yaml:"checkpoint" json:"checkpoint" koanf:"checkpoint"`
	TaskSlot   TaskSlotConfig   `yaml:"task_slot" json:"task_slot" koanf:"task_slot"`
	Mode       string           `yaml:"mode"        json:"mode"        koanf:"mode"`
	Listen     string           `yaml:"listen"      json:"listen"      koanf:"listen"`
	Node       NodeConfig       `yaml:"node"        json:"node"        koanf:"node"`
	HTTP       HTTPConfig       `yaml:"http"        json:"http"        koanf:"http"`
	NodeTLS    TLSConfig        `yaml:"node_tls"    json:"node_tls"    koanf:"node_tls"`
	Auth       AuthConfig       `yaml:"auth"        json:"auth"        koanf:"auth"`
	WriteQueue WriteQueueConfig `yaml:"write_queue" json:"write_queue" koanf:"write_queue"`
	Election   ElectionConfig   `yaml:"election"    json:"election"    koanf:"election"`
	Worker     WorkerConfig     `yaml:"worker"      json:"worker"      koanf:"worker"`
}

type CheckpointConfig struct {
	MinPause               Duration `yaml:"min_pause" json:"min_pause" koanf:"min_pause"`
	TolerableFailureRate   float64  `yaml:"tolerable_failure_rate" json:"tolerable_failure_rate" koanf:"tolerable_failure_rate"`
	MaxConsecutiveFailures int      `yaml:"max_consecutive_failures" json:"max_consecutive_failures" koanf:"max_consecutive_failures"`
	Timeout                Duration `yaml:"timeout" json:"timeout" koanf:"timeout"`
}

// WorkerConfig holds settings for running in worker mode.
type WorkerConfig struct {
	CheckpointReplica CheckpointReplicaConfig `yaml:"checkpoint_replica" json:"checkpoint_replica" koanf:"checkpoint_replica"`
	CoordinatorAddr   string                  `yaml:"coordinator_addr" json:"coordinator_addr" koanf:"coordinator_addr"`
	WorkerID          string                  `yaml:"worker_id"        json:"worker_id"        koanf:"worker_id"`
	ListenAddr        string                  `yaml:"listen_addr"      json:"listen_addr"      koanf:"listen_addr"`
	TaskSlots         int                     `yaml:"task_slots"       json:"task_slots"       koanf:"task_slots"`
}

// CheckpointReplicaConfig enables peer checkpoint storage when ListenAddr is set.
// Storage directories must already exist and be owned by this worker.
type CheckpointReplicaConfig struct {
	ListenAddr    string `yaml:"listen_addr" json:"listen_addr" koanf:"listen_addr"`
	AdvertiseAddr string `yaml:"advertise_addr" json:"advertise_addr" koanf:"advertise_addr"`
	StoreRoot     string `yaml:"store_root" json:"store_root" koanf:"store_root"`
	ArtifactRoot  string `yaml:"artifact_root" json:"artifact_root" koanf:"artifact_root"`
	StagingRoot   string `yaml:"staging_root" json:"staging_root" koanf:"staging_root"`
	Concurrency   int    `yaml:"concurrency" json:"concurrency" koanf:"concurrency"`
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

// TaskSlotConfig controls bounded task channels and checkpoint uploads.
type TaskSlotConfig struct {
	InputBufferSize             int      `yaml:"input_buffer_size" json:"input_buffer_size" koanf:"input_buffer_size"`
	OutputBufferSize            int      `yaml:"output_buffer_size" json:"output_buffer_size" koanf:"output_buffer_size"`
	AlignmentBufferSize         int      `yaml:"alignment_buffer_size" json:"alignment_buffer_size" koanf:"alignment_buffer_size"`
	CheckpointUploadConcurrency int      `yaml:"checkpoint_upload_concurrency" json:"checkpoint_upload_concurrency" koanf:"checkpoint_upload_concurrency"`
	DrainTimeout                Duration `yaml:"drain_timeout" json:"drain_timeout" koanf:"drain_timeout"`
}

// HeartbeatConfig controls worker liveness, independently of RPC call timeouts.
type HeartbeatConfig struct {
	Interval    Duration `yaml:"interval" json:"interval" koanf:"interval"`
	Timeout     Duration `yaml:"timeout" json:"timeout" koanf:"timeout"`
	MaxFailures int      `yaml:"max_failures" json:"max_failures" koanf:"max_failures"`
}
