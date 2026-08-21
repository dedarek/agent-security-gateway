package config

// Config is the Gateway runtime configuration.
type Config struct {
	Listen          string
	CedarPolicyPath string   // permission axis (ToolHive/Cedar)
	RulesPath       string   // data/network axis (Pipelock rule bundle)
	UpstreamCommand []string // real upstream MCP server process (argv)

	// Behavior/causal axis (real content-based taint).
	TaintSources []string // tool names whose output is untrusted
	TaintSinks   []string // tool names that egress data

	IncludeExperimentalRules bool
}

// Default returns config for the MVP demo (paths relative to repo root).
func Default() Config {
	return Config{
		Listen:                   ":8080",
		CedarPolicyPath:          "./deploy/policies/permission.cedar",
		RulesPath:                "./deploy/rules/pipelock-community.yaml",
		UpstreamCommand:          []string{"./bin/upstream-mcp"},
		TaintSources:             []string{"get_inbox", "read_secret", "fetch", "read_file"},
		TaintSinks:               []string{"send_email", "http_post", "export_all_users"},
		IncludeExperimentalRules: false,
	}
}
