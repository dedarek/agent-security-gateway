package config

// Config is the Gateway runtime configuration.
type Config struct {
	Listen                   string
	CedarPolicyPath          string // permission axis (ToolHive/Cedar)
	RulesPath                string // data/network axis (Pipelock rule bundle)
	BehaviorSidecar          string // behavior axis (Invariant Python sidecar base URL)
	IncludeExperimentalRules bool
}

// Default returns config for the MVP demo (paths relative to repo root).
func Default() Config {
	return Config{
		Listen:                   ":8080",
		CedarPolicyPath:          "./deploy/policies/permission.cedar",
		RulesPath:                "./deploy/rules/pipelock-community.yaml",
		BehaviorSidecar:          "http://127.0.0.1:8900",
		IncludeExperimentalRules: false,
	}
}
