package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// registryEntry mirrors the central registry Entry.
type registryEntry struct {
	Name    string   `json:"name"`
	Command []string `json:"command,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// syncLoop polls the hub's registry for this tenant and auto-mounts MCP
// servers into local agent configs whenever the set changes. This is the
// "admin curates centrally, users get tools automatically" loop.
func syncLoop(cfg *ProbeConfig, stop <-chan struct{}) {
	if cfg.HubURL == "" || cfg.TenantName == "" {
		return
	}
	lastHash := ""
	for {
		func() {
			defer func() { _ = recover() }()
			url := strings.TrimSuffix(cfg.HubURL, "/") +
				"/api/registry/sync?tenant=" + cfg.TenantName
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				return
			}
			req.Header.Set("Authorization", "Bearer "+cfg.TenantKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return // offline; retry next tick (spool keeps events safe)
			}
			defer resp.Body.Close()
			var out struct {
				Hash    string          `json:"hash"`
				Entries []registryEntry `json:"entries"`
			}
			if json.NewDecoder(resp.Body).Decode(&out) != nil {
				return
			}
			if out.Hash == lastHash {
				return // unchanged
			}
			if err := mountMCP(cfg, out.Entries); err != nil {
				logPrintf("mount failed: %v", err)
				return
			}
			lastHash = out.Hash
			logPrintf("registry synced: %d mcp servers mounted (hash %s)", len(out.Entries), out.Hash)
		}()
		select {
		case <-stop:
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// mountMCP writes the entries into every supported agent config file that
// exists on this machine (claude code .mcp.json / codex config are handled by
// their own scopes; the common denominator is the project .mcp.json format).
func mountMCP(cfg *ProbeConfig, entries []registryEntry) error {
	home, _ := os.UserHomeDir()
	targets := []string{
		filepath.Join(home, ".claude", "mcp.json"),
		filepath.Join(home, ".cursor", "mcp.json"),
	}
	for _, path := range targets {
		if dir := filepath.Dir(path); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		doc := map[string]any{"mcpServers": map[string]any{}}
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &doc)
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		for _, e := range entries {
			name := "asg-" + e.Name
			switch {
			case len(e.Command) > 0:
				servers[name] = map[string]any{
					"command": e.Command[0],
					"args":    e.Command[1:],
				}
			case e.URL != "":
				servers[name] = map[string]any{
					"type": "http",
					"url":  probeWrapURL(e.URL, cfg),
				}
			}
		}
		doc["mcpServers"] = servers
		b, _ := json.MarshalIndent(doc, "", "  ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		logPrintf("mounted %d servers -> %s", len(entries), path)
	}
	return nil
}

// probeWrapURL points remote MCP URLs at the local probe shim so tenant keys
// stay out of agent configs. The shim currently targets the central gateway;
// remote third-party servers would each need their own shim route (roadmap).
func probeWrapURL(rawURL string, cfg *ProbeConfig) string {
	return "http://" + cfg.Listen + "/mcp"
}

var logPrintf = func(f string, a ...any) { fmt.Fprintf(osStderr, f+"\n", a...) }

var osStderr = os.Stderr
