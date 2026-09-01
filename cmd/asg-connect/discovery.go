package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/db"
	"github.com/dedarek/agent-security-gateway/internal/inventory"
)

const maxDiscoveryFiles = 5000

func discoverLocalRoots(agentID string, roots []string) ([]db.InventoryItem, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("empty agent id")
	}
	items := make([]db.InventoryItem, 0)
	seen := map[string]bool{}
	files := 0
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return nil // discovery is best-effort; other roots still matter
			}
			if entry.IsDir() {
				if path != root && (excludedDiscoveryDir(entry.Name()) || discoveryDepth(root, path) > 5) {
					return fs.SkipDir
				}
				return nil
			}
			files++
			if files > maxDiscoveryFiles {
				return fs.SkipAll
			}
			name := entry.Name()
			if strings.EqualFold(name, "SKILL.md") {
				content, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				item := inventory.DiscoverSkillFile(agentID, path, content)
				if !seen[item.StableKey] {
					seen[item.StableKey] = true
					items = append(items, item)
				}
				return nil
			}
			if !isMCPConfigCandidate(name) {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			found, err := inventory.DiscoverConfigFile(agentID, path, raw)
			if err != nil {
				return nil // unrelated config files are normal
			}
			for _, item := range found {
				if !seen[item.StableKey] {
					seen[item.StableKey] = true
					items = append(items, item)
				}
			}
			return nil
		})
		if err != nil && err != fs.SkipAll {
			return nil, err
		}
		if files >= maxDiscoveryFiles {
			break
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].StableKey < items[j].StableKey
	})
	return items, nil
}

func postInventory(client *http.Client, hubURL string, item db.InventoryItem) error {
	body, err := json.Marshal(item)
	if err != nil {
		return err
	}
	url := strings.TrimRight(hubURL, "/") + "/api/inventory/ingest"
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("inventory ingest returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func startInventoryDiscovery(cfg *ProbeConfig) {
	if cfg == nil || strings.TrimSpace(cfg.HubURL) == "" || strings.TrimSpace(cfg.AgentID) == "" {
		return
	}
	roots := cfg.DiscoveryRoots
	if len(roots) == 0 {
		roots = defaultDiscoveryRoots()
	}
	go func() {
		client := &http.Client{Timeout: 15 * time.Second}
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			discoverAndReport(client, cfg, roots)
			<-ticker.C
		}
	}()
}

func discoverAndReport(client *http.Client, cfg *ProbeConfig, roots []string) {
	items, err := discoverLocalRoots(cfg.AgentID, roots)
	if err != nil {
		logPrintf("inventory discovery failed: %v", err)
		return
	}
	for _, item := range items {
		if err := postInventory(client, cfg.HubURL, item); err != nil {
			logPrintf("inventory report deferred: %v", err)
			return
		}
	}
	if len(items) > 0 {
		logPrintf("inventory discovery reported %d observations", len(items))
	}
}

func defaultDiscoveryRoots() []string {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	roots := []string{}
	if home != "" {
		roots = append(roots, home)
	}
	if cwd != "" && cwd != home {
		roots = append(roots, cwd)
	}
	return roots
}

func discoveryDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}

func excludedDiscoveryDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".cache", ".npm", ".pnpm-store", "node_modules", "vendor", "dist", "build", "appdata":
		return true
	default:
		return false
	}
}

func isMCPConfigCandidate(name string) bool {
	lower := strings.ToLower(name)
	ext := strings.ToLower(filepath.Ext(lower))
	if ext != ".json" && ext != ".yaml" && ext != ".yml" && ext != ".toml" {
		return false
	}
	return strings.Contains(lower, "config") || strings.Contains(lower, "settings") || strings.Contains(lower, "mcp")
}
