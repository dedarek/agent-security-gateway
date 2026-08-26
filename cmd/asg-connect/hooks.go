package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// hookCheck implements the Claude Code / Cursor hook protocol: JSON on stdin
// describing the tool call, exit code decides (0=allow, 2=block).
func hookCheck(args []string) error {
	raw, err := ioReadAll(os.Stdin)
	if err != nil {
		return err
	}
	var req struct {
		SessionID string          `json:"session_id"`
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}

	cfg, err := loadProbeConfig("connect.yaml")
	if err != nil {
		return err
	}
	rep := newReporter(cfg.HubURL, cfg.TenantKey, cfg.EventSpoolPath, cfg.TenantName)
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "hook-" + cfg.TenantName
	}

	verdict, reason := localVerdict(req.ToolName, req.ToolInput)
	rep.ReportTool(sessionID, "hook."+req.ToolName, req.ToolInput, verdict, reason)

	switch verdict {
	case "BLOCK":
		fmt.Printf(`{"decision":"block","reason":%q}`, reason)
		fmt.Println()
		os.Exit(2)
	default: // ALLOW (+ still reported for audit)
		os.Exit(0)
	}
	return nil
}

// localVerdict is the offline rule pack: fast, deterministic checks that run
// on the machine without contacting the hub.
func localVerdict(tool string, input json.RawMessage) (verdict, reason string) {
	s := strings.ToLower(string(input))
	blocked := []string{
		"rm -rf /", "drop table", "shutdown", ":(){", "mkfs",
		"attacker@gmail.com", // demo taint sink; real pack synced from hub
	}
	for _, b := range blocked {
		if strings.Contains(s, b) {
			return "BLOCK", "local policy: dangerous pattern " + b
		}
	}
	// secrets about to leave the machine
	if tool == "webfetch" || tool == "bash" || tool == "send_email" {
		for _, pat := range []string{"sk-", "ghp_", "ops_", "api_key"} {
			if strings.Contains(s, pat) && (strings.Contains(s, "http") || strings.Contains(tool, "email")) {
				return "REDACT", "possible secret in outbound payload (" + pat + "...)"
			}
		}
	}
	return "ALLOW", ""
}

func ioReadAll(f *os.File) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := f.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}

// initClient rewrites an agent's config to route through the probe.
func initClient(app string) error {
	cfg, err := loadProbeConfig("connect.yaml")
	if err != nil {
		return fmt.Errorf("load connect.yaml first: %w", err)
	}
	home, _ := os.UserHomeDir()
	switch app {
	case "claude-code":
		// Claude Code reads managed settings; we write the user settings file
		// adding env so every launch routes via the probe.
		dir := filepath.Join(home, ".claude")
		_ = os.MkdirAll(dir, 0o755)
		settings := map[string]any{
			"env": map[string]string{
				"ANTHROPIC_BASE_URL": "http://" + cfg.Listen,
				"ANTHROPIC_AUTH_TOKEN": firstKey(cfg),
			},
		}
		b, _ := json.MarshalIndent(settings, "", "  ")
		p := filepath.Join(dir, "settings.json")
		if err := mergeJSONFile(p, b); err != nil {
			return err
		}
		fmt.Println("claude-code configured:", p, "-> probe", cfg.Listen)

	case "codex":
		dir := filepath.Join(home, ".codex")
		_ = os.MkdirAll(dir, 0o755)
		toml := fmt.Sprintf(`model_provider = "asg"
model = %q

[model_providers.asg]
name = "ASG Probe"
base_url = "http://%s/v1"
wire_api = "chat"
`, firstDefaultModel(cfg), cfg.Listen)
		p := filepath.Join(dir, "config.toml")
		if err := appendIfAbsent(p, "\n# --- added by asg-connect ---\n"+toml); err != nil {
			return err
		}
		_ = writeAuthFile(filepath.Join(dir, "auth.json"), firstKey(cfg))
		fmt.Println("codex configured:", p, "-> probe", cfg.Listen)

	case "cursor":
		fmt.Println("cursor: edit ~/.cursor/mcp.json — set mcp server url to http://" + cfg.Listen + "/mcp")

	default:
		return fmt.Errorf("unknown app %q", app)
	}
	_ = exec.Command // keep import
	return nil
}

func firstKey(cfg *ProbeConfig) string {
	if len(cfg.Providers) > 0 {
		return cfg.Providers[0].APIKey
	}
	return ""
}

func firstDefaultModel(cfg *ProbeConfig) string {
	if len(cfg.Providers) > 0 && cfg.Providers[0].DefaultModel != "" {
		return cfg.Providers[0].DefaultModel
	}
	return "gpt-4o-mini"
}

// mergeJSONFile merges "env" keys into an existing settings.json without
// destroying other content.
func mergeJSONFile(path string, newBytes []byte) error {
	var existing map[string]any
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &existing)
	}
	var add map[string]any
	if err := json.Unmarshal(newBytes, &add); err != nil {
		return err
	}
	if existing == nil {
		existing = map[string]any{}
	}
	for k, v := range add {
		if m, ok := v.(map[string]any); ok {
			cur, _ := existing[k].(map[string]any)
			if cur == nil {
				cur = map[string]any{}
			}
			for k2, v2 := range m {
				cur[k2] = v2
			}
			existing[k] = cur
		} else {
			existing[k] = v
		}
	}
	out, _ := json.MarshalIndent(existing, "", "  ")
	return os.WriteFile(path, out, 0o644)
}

func appendIfAbsent(path, text string) error {
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "asg-connect") {
		return nil // already configured
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

func writeAuthFile(path, key string) error {
	auth := map[string]string{"OPENAI_API_KEY": key}
	b, _ := json.MarshalIndent(auth, "", "  ")
	return os.WriteFile(path, b, 0o600)
}
