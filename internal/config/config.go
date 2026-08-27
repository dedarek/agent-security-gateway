package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the Gateway runtime configuration. It can be built programmatically
// (Default) or loaded from a YAML file (Load).
type Config struct {
	Listen          string   `yaml:"listen"`
	LLMUpstreamURL  string   `yaml:"llm_upstream_url"`
	CedarPolicyPath string   `yaml:"cedar_policy_path"`
	RulesPath       string   `yaml:"rules_path"`
	UpstreamCommand []string `yaml:"upstream_command"`

	// Behavior/causal axis (content-based taint): tool names whose output is
	// untrusted, and tool names that egress data.
	TaintSources []string `yaml:"taint_sources"`
	TaintSinks   []string `yaml:"taint_sinks"`

	// Pipelock rule bundles: experimental rules off by default; extra trusted
	// third-party signing keys (comma-separated hex Ed25519 public keys).
	IncludeExperimentalRules bool   `yaml:"include_experimental_rules"`
	ExtraTrustedKeysHex      string `yaml:"extra_trusted_keys_hex"`

	// Behavior sidecar (Invariant analyzer). Empty => engine disabled.
	BehaviorSidecarURL string `yaml:"behavior_sidecar_url"`
	BehaviorFailOpen   bool   `yaml:"behavior_fail_open"`

	// Operations: event log (JSONL) for the Intelligence plane, operator UI
	// listener, approval timeout, multi-tenant registry file.
	EventLogPath    string        `yaml:"event_log_path"`
	UIListen        string        `yaml:"ui_listen"`
	ApprovalTimeout time.Duration `yaml:"approval_timeout"`
	TenantsPath     string        `yaml:"tenants_path"`

	// Semantica KG bridge (optional; empty worker script = disabled).
	KGPythonBin     string `yaml:"kg_python_bin"`
	KGWorkerScript  string `yaml:"kg_worker_script"`
	KGSemanticaPath string `yaml:"kg_semantica_path"`
	KGPort          int    `yaml:"kg_port"`

	// Semantica Explorer (interactive graph visualization), proxied into the
	// console at /explorer/. Empty URL = link out instead of embed.
	ExplorerURL    string `yaml:"explorer_url"`
	ExplorerAPIKey string `yaml:"explorer_api_key"`
}

// Default returns config for the MVP demo (paths relative to repo root).
func Default() Config {
	return Config{
		Listen:                   ":8080",
		LLMUpstreamURL:           "http://127.0.0.1:8181",
		CedarPolicyPath:          "./deploy/policies/permission.cedar",
		RulesPath:                "./deploy/rules/pipelock-community.yaml",
		UpstreamCommand:          []string{"./bin/upstream-mcp"},
		TaintSources:             []string{"get_inbox", "read_secret", "fetch", "read_file"},
		TaintSinks:               []string{"send_email", "http_post", "export_all_users"},
		IncludeExperimentalRules: false,
		EventLogPath:             "./data/events.jsonl",
		UIListen:                 ":8090",
		ApprovalTimeout:          120 * time.Second,
		KGPythonBin:              "./.venv-kg/bin/python",
		KGWorkerScript:           "internal/kgbridge/asg_kg_worker.py",
		KGSemanticaPath:          "", // set to semantica checkout if used
		KGPort:                   8902,
		ExplorerURL:              "http://127.0.0.1:8091",
		ExplorerAPIKey:           "asg-explorer-key",
	}
}

// Load reads a YAML config file over the defaults (missing fields keep their
// default values), so a minimal config file only overrides what it states.
func Load(path string) (Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}
