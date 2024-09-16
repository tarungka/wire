package sinks


type SinkConfig struct {
	Name           string            `koanf:"name" json:"name"`
	ConnectionType string            `koanf:"type" json:"type"`
	Config         map[string]string `koanf:"config" json:"config"`
	Key            string            `koanf:"key" json:"key"`
}