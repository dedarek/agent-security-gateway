package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
//
// Universal onboarding (harness-agnostic): new installs write
// ~/.config/asg/universal.json with only hub_url / tenant_key / listen.
// Providers is kept for backward compatibility with legacy connect.yaml;
// new universal.json may omit Providers entirely — model is sniffed from
// traffic (observed_model) and AgentAlias no longer derives from
// Providers[0].DefaultModel.
type ProbeConfig struct {
	Listen string `yaml:"listen" json:"listen"` // local proxy listen addr, e.g. 127.0.0.1:8181

	// Upstream LLM providers the user configured (Bifrost-style model freedom).
	// Kept for compatibility with legacy connect.yaml; universal.json may omit.
	Providers []Provider `yaml:"providers" json:"providers,omitempty"`

	// Central gateway coordinates.
	HubURL     string `yaml:"hub_url" json:"hub_url"`       // e.g. http://gw.corp:8080
	TenantKey  string `yaml:"tenant_key" json:"tenant_key"` // this machine's identity
	TenantName string `yaml:"tenant_name" json:"tenant_name"`
	AgentID    string `yaml:"agent_id" json:"agent_id"`
	AgentType  string `yaml:"agent_type" json:"agent_type"`
	AgentAlias string `yaml:"agent_alias" json:"agent_alias"`
	DeclaredIPs []string `yaml:"declared_ips,omitempty" json:"declared_ips,omitempty"`

	// UniversalPath is the filesystem path of the universal declaration
	// (~/.config/asg/universal.json). Not required in the file itself;
	// loadProbeConfig fills it with the resolved path. Kept here so
	// callers can distinguish universal vs legacy sources.
	UniversalPath string `yaml:"universal_path" json:"universal_path"`

	// MCP shim: real upstream MCP servers, re-published locally.
	MCPUpstreams []MCPUpstream `yaml:"mcp_upstreams" json:"mcp_upstreams,omitempty"`

	// Local policy pack cache (synced from hub; offline enforcement).
	PolicyCachePath string `yaml:"policy_cache_path" json:"policy_cache_path"`
	EventSpoolPath  string `yaml:"event_spool_path" json:"event_spool_path"`

	// Semantic scanner LLM settings.
	Semantic struct {
		LLMMaxTokens      int `yaml:"llm_max_tokens" json:"llm_max_tokens"`             // inline: keep small for speed
		AsyncLLMMaxTokens int `yaml:"async_llm_max_tokens" json:"async_llm_max_tokens"` // 0 = unlimited (post-hoc)
	} `yaml:"semantic" json:"semantic"`
}

type Provider struct {
	Name    string `yaml:"name" json:"name"`         // logical name, e.g. "opencode-zen"
	BaseURL string `yaml:"base_url" json:"base_url"` // real provider endpoint
	APIKey  string `yaml:"api_key" json:"api_key"`
	// DefaultModel is used when the agent sends a model name the probe maps
	// to this provider; per-model mapping can override.
	DefaultModel string            `yaml:"default_model,omitempty" json:"default_model,omitempty"`
	ModelMap     map[string]string `yaml:"model_map,omitempty" json:"model_map,omitempty"` // agent-visible -> upstream model id

	// AllowedModels restricts which model names may be forwarded (quota guard).
	// Empty slice = first provider default only.
	AllowedModels []string `yaml:"allowed_models,omitempty" json:"allowed_models,omitempty"`
}

type MCPUpstream struct {
	Name    string   `yaml:"name" json:"name"`       // published tool namespace
	Command []string `yaml:"command" json:"command"` // argv to spawn the real server
}

// DefaultUniversalPath returns the conventional universal declaration path.
func DefaultUniversalPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return filepath.Join(".config", "asg", "universal.json")
	}
	return filepath.Join(home, ".config", "asg", "universal.json")
}

func loadProbeConfig(path string) (*ProbeConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// expand ${ENV} in the file so keys stay out of config files
	expanded := osExpandEnv(string(raw))
	var cfg ProbeConfig
	isJSON := strings.HasSuffix(strings.ToLower(strings.TrimSpace(path)), ".json")
	if isJSON {
		if err := json.Unmarshal([]byte(expanded), &cfg); err != nil {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
			return nil, err
		}
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8181"
	}
	if cfg.EventSpoolPath == "" {
		cfg.EventSpoolPath = "./connect-events.jsonl"
	}
	// Record resolved universal path for callers; do not overwrite if already set in file.
	if cfg.UniversalPath == "" {
		if isJSON {
			cfg.UniversalPath = path
		} else if path == DefaultUniversalPath() {
			cfg.UniversalPath = path
		}
	}
	return &cfg, nil
}
