package main

import (
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

var envRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func osExpandEnv(s string) string {
	return envRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		return os.Getenv(name)
	})
}

// ProbeConfig is the local probe configuration. The user's own model choice
// and provider key live HERE (not in the agent), so the user keeps full
// freedom over models while the probe owns routing + reporting.
type ProbeConfig struct {
	Listen string `yaml:"listen"` // local proxy listen addr, e.g. 127.0.0.1:8181

	// Upstream LLM providers the user configured (Bifrost-style model freedom).
	Providers []Provider `yaml:"providers"`

	// Central gateway coordinates.
	HubURL     string `yaml:"hub_url"`      // e.g. http://gw.corp:8080
	TenantKey  string `yaml:"tenant_key"`   // this machine's identity
	TenantName string `yaml:"tenant_name"`

	// MCP shim: real upstream MCP servers, re-published locally.
	MCPUpstreams []MCPUpstream `yaml:"mcp_upstreams"`

	// Local policy pack cache (synced from hub; offline enforcement).
	PolicyCachePath string `yaml:"policy_cache_path"`
	EventSpoolPath  string `yaml:"event_spool_path"`
}

type Provider struct {
	Name    string `yaml:"name"`    // logical name, e.g. "opencode-zen"
	BaseURL string `yaml:"base_url"` // real provider endpoint
	APIKey  string `yaml:"api_key"`
	// DefaultModel is used when the agent sends a model name the probe maps
	// to this provider; per-model mapping can override.
	DefaultModel string            `yaml:"default_model,omitempty"`
	ModelMap     map[string]string `yaml:"model_map,omitempty"` // agent-visible -> upstream model id

	// AllowedModels restricts which model names may be forwarded (quota guard).
	// Empty slice = first provider default only.
	AllowedModels []string `yaml:"allowed_models,omitempty"`
}

type MCPUpstream struct {
	Name    string   `yaml:"name"`   // published tool namespace
	Command []string `yaml:"command"` // argv to spawn the real server
}

func loadProbeConfig(path string) (*ProbeConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// expand ${ENV} in the YAML so keys stay out of config files
	expanded := osExpandEnv(string(raw))
	var cfg ProbeConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, err
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8181"
	}
	if cfg.EventSpoolPath == "" {
		cfg.EventSpoolPath = "./connect-events.jsonl"
	}
	return &cfg, nil
}
