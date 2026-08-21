package config

// Config is the Gateway runtime configuration. Loaded from YAML in production;
// see deploy/config.dev.yaml.
type Config struct {
	Listen   string         `yaml:"listen"`   // e.g. ":8080"
	Upstream string         `yaml:"upstream"` // real MCP server address
	Policy   PolicyConfig   `yaml:"policy"`
	Engines  []EngineToggle `yaml:"engines"`
	Audit    AuditConfig    `yaml:"audit"`
}

type PolicyConfig struct {
	CedarPath string `yaml:"cedar_path"` // path to .cedar policy files (hot-reloaded)
}

type EngineToggle struct {
	Name    string `yaml:"name"`
	Enabled bool   `yaml:"enabled"`
}

type AuditConfig struct {
	DSN   string `yaml:"dsn"`   // postgres/clickhouse; empty => stdout
	Topic string `yaml:"topic"` // NATS/Kafka topic; empty => no publish
}

// Default returns a minimal config suitable for the MVP demo.
func Default() Config {
	return Config{
		Listen:   ":8080",
		Upstream: "http://127.0.0.1:9000",
		Policy:   PolicyConfig{CedarPath: "./deploy/policies"},
		Engines: []EngineToggle{
			{Name: "permission.cedar-stub", Enabled: true},
		},
	}
}
