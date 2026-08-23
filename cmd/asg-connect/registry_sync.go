package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
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
// exists on this machine. Cross-platform safety:
//   - Windows-only commands (D:/... exe) are skipped on non-Windows and
//     vice versa, preventing broken entries from being mounted.
//   - A .bak backup of the existing config is created before first write.
func mountMCP(cfg *ProbeConfig, entries []registryEntry) error {
	home, _ := os.UserHomeDir()
	isWindows := runtime.GOOS == "windows"
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
			_ = os.WriteFile(path+".bak", b, 0o644) // backup before modify
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		for _, e := range entries {
			name := "asg-" + e.Name
			switch {
			case len(e.Command) > 0:
				// cross-platform guard: skip commands whose binary path
				// doesn't match the current OS convention
				bin := e.Command[0]
				cmdIsWin := strings.Contains(bin, ":/") || strings.HasSuffix(strings.ToLower(bin), ".exe")
				if cmdIsWin != isWindows {
					continue // wrong platform; skip silently
				}
				servers[name] = map[string]any{
					"command": e.Command[0],
					"args":    e.Command[1:],
				}
			case e.URL != "":
				servers[name] = map[string]any{
					"type": "http",
					"url":  probeWrapURL(e.URL, cfg),
				}
			default:
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
		logPrintf("mounted -> %s (backup at %s.bak)", path, path)
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
