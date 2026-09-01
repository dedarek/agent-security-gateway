package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CoverageReport is the self-check a probe sends to the hub every 30s.
type CoverageReport struct {
	AgentID        string   `json:"agent_id"`
	AgentType      string   `json:"agent_type"`
	ProxyUp        bool     `json:"proxy_up"`
	HubReachable   bool     `json:"hub_reachable"`
	HookPresent    bool     `json:"hook_present"`
	HookConfigured bool     `json:"hook_configured"`
	PostHookCfg    bool     `json:"posthook_configured"`
	ToolsCovered   []string `json:"tools_covered"`
	ToolsPartial   []string `json:"tools_partial"`
	ReportedAt     int64    `json:"reported_at"`
}

// hookScriptPaths are the candidate locations for the Claude PreToolUse hook.
var hookScriptPaths = []string{
	"~/.asg-hook.sh",
	"/home/server/.asg-hook.sh",
	"/root/.asg-hook.sh",
}

// claudeSettingsPaths are candidate Claude Code settings files.
var claudeSettingsPaths = []string{
	"~/.claude/settings.json",
	"/home/server/.claude/settings.json",
}

func expandHome(p string) string {
	if p == "~" {
		if h, err := os.UserHomeDir(); err == nil {
			return h
		}
	}
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}

// hookHashFile is where the expected hook script hash is stored.
func hookHashFile() string {
	return filepath.Join(configDir(), "hook.sha256")
}

func configDir() string {
	if d := os.Getenv("ASG_CONFIG_DIR"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ".config/asg"
	}
	return filepath.Join(h, ".config", "asg")
}

func fileExists(p string) bool {
	_, err := os.Stat(expandHome(p))
	return err == nil
}

func fileHash(p string) (string, error) {
	b, err := os.ReadFile(expandHome(p))
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// checkHookPresence verifies the hook script exists (and hash-matches when a
// reference hash was recorded).
func checkHookPresence() (present, hashOK bool) {
	var found string
	for _, p := range hookScriptPaths {
		if fileExists(p) {
			found = p
			break
		}
	}
	if found == "" {
		return false, false
	}
	ref, err := os.ReadFile(hookHashFile())
	if err != nil || len(ref) < 32 {
		// no reference hash recorded yet — presence is enough
		return true, true
	}
	cur, err := fileHash(found)
	if err != nil {
		return true, false
	}
	return true, strings.TrimSpace(string(ref)) == cur
}

// checkSettingsHook verifies the Claude settings.json wires the hook script
// into PreToolUse (and optionally PostToolUse).
func checkSettingsHook() (pre, post bool) {
	for _, p := range claudeSettingsPaths {
		b, err := os.ReadFile(expandHome(p))
		if err != nil {
			continue
		}
		var s struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		for name, arr := range s.Hooks {
			for _, m := range arr {
				for _, h := range m.Hooks {
					if h.Command == "" {
						continue
					}
					if strings.Contains(h.Command, "asg-hook") {
						if name == "PreToolUse" {
							pre = true
						}
						if name == "PostToolUse" {
							post = true
						}
					}
				}
			}
		}
		if pre {
			break
		}
	}
	return pre, post
}

// collectCoverage runs the local self-checks.
func collectCoverage(cfg *ProbeConfig, hubReachable bool) CoverageReport {
	hookPresent, _ := checkHookPresence()
	preCfg, postCfg := checkSettingsHook()
	return CoverageReport{
		AgentID:        cfg.AgentID,
		AgentType:      cfg.AgentType,
		ProxyUp:        true, // we are running
		HubReachable:   hubReachable,
		HookPresent:    hookPresent,
		HookConfigured: preCfg,
		PostHookCfg:    postCfg,
		ToolsCovered:   []string{"Bash", "Read", "Write", "Edit", "WebFetch"},
		ToolsPartial:   []string{"MCP"},
		ReportedAt:     time.Now().UnixMilli(),
	}
}

// reportCoverage POSTs the report to the hub.
func reportCoverage(cfg *ProbeConfig, rep *reporter) {
	body, _ := json.Marshal(collectCoverage(cfg, rep.hubReachable()))
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimSuffix(cfg.HubURL, "/")+"/api/coverage/report", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// coverageLoop runs every 30s until stop is closed.
func coverageLoop(cfg *ProbeConfig, rep *reporter, stop chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// immediate first report
	reportCoverage(cfg, rep)
	for {
		select {
		case <-ticker.C:
			reportCoverage(cfg, rep)
		case <-stop:
			return
		}
	}
}

// hubReachable probes the hub health endpoint.
func (r *reporter) hubReachable() bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(r.hubURL, "/") + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}
