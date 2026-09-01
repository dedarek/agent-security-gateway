// Package inventory normalizes capability observations from any agent
// harness into the gateway's shared inventory model. It intentionally has no
// Codex/Claude/OpenCode branches.
package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/dedarek/agent-security-gateway/internal/db"
	"gopkg.in/yaml.v3"
)

// DiscoverConfigFile parses a JSON, YAML, or TOML configuration file and
// extracts protocol-level MCP declarations. The file name is only provenance;
// it does not select a harness-specific parser.
func DiscoverConfigFile(agentID, configPath string, raw []byte) ([]db.InventoryItem, error) {
	ext := strings.ToLower(filepath.Ext(configPath))
	if ext == ".json" || ext == "" {
		return DiscoverMCPJSON(agentID, configPath, raw)
	}
	var document any
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &document); err != nil {
			return nil, fmt.Errorf("parse MCP YAML %s: %w", configPath, err)
		}
	case ".toml":
		var parsed map[string]any
		if _, err := toml.Decode(string(raw), &parsed); err != nil {
			return nil, fmt.Errorf("parse MCP TOML %s: %w", configPath, err)
		}
		document = parsed
	default:
		return nil, fmt.Errorf("unsupported MCP config format %s", ext)
	}
	jsonBytes, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("normalize MCP config %s: %w", configPath, err)
	}
	return DiscoverMCPJSON(agentID, configPath, jsonBytes)
}

// DiscoverMCPJSON extracts MCP server declarations from a JSON document. The
// surrounding document may be a harness config, a project config, or the
// generic ASG config; only the protocol-level mcpServers key matters.
func DiscoverMCPJSON(agentID, configPath string, raw []byte) ([]db.InventoryItem, error) {
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse MCP JSON %s: %w", configPath, err)
	}
	var out []db.InventoryItem
	seen := map[string]bool{}
	walkMCPMaps(document, func(name string, spec map[string]any) {
		key := strings.TrimSpace(name)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		item := normalizeServer(agentID, configPath, key, spec)
		out = append(out, item)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DiscoverSkillFile turns one skill document into an inventory item. The
// content is represented by a hash; callers should send only sanitized
// excerpts to any later LLM analysis job.
func DiscoverSkillFile(agentID, path string, content []byte) db.InventoryItem {
	name := skillName(path, content)
	hash := sha256.Sum256(content)
	return db.InventoryItem{
		StableKey:        db.StableInventoryKey(agentID, "skill", path, name),
		AgentID:          agentID,
		Kind:             "skill",
		Name:             name,
		Source:           "filesystem",
		Origin:           path,
		InstallPath:      path,
		ManifestHash:     "sha256:" + hex.EncodeToString(hash[:]),
		Status:           "pending_review",
		AIAnalysisStatus: "pending",
	}
}

func walkMCPMaps(value any, emit func(string, map[string]any)) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if strings.EqualFold(key, "mcpServers") || strings.EqualFold(key, "mcp_servers") {
				if servers, ok := child.(map[string]any); ok {
					for name, rawSpec := range servers {
						if spec, ok := rawSpec.(map[string]any); ok {
							emit(name, spec)
						}
					}
				}
			}
			walkMCPMaps(child, emit)
		}
	case []any:
		for _, child := range node {
			walkMCPMaps(child, emit)
		}
	}
}

func normalizeServer(agentID, configPath, name string, spec map[string]any) db.InventoryItem {
	command := stringValue(spec["command"])
	args := stringSlice(spec["args"])
	url := stringValue(spec["url"])
	origin := url
	declared := []string{}
	if command != "" {
		origin = strings.Join(append([]string{command}, args...), " ")
		declared = append(declared, "process")
	}
	if url != "" {
		declared = append(declared, "external-network")
	}
	entry, _ := json.Marshal(spec)
	hash := sha256.Sum256(entry)
	return db.InventoryItem{
		StableKey:        db.StableInventoryKey(agentID, "mcp_server", configPath, name),
		AgentID:          agentID,
		Kind:             "mcp_server",
		Name:             name,
		Source:           "config",
		Origin:           origin,
		InstallPath:      configPath,
		ManifestHash:     "sha256:" + hex.EncodeToString(hash[:]),
		DeclaredCaps:     declared,
		Status:           "pending_review",
		AIAnalysisStatus: "pending",
	}
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s := stringValue(value); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var skillNameRe = regexp.MustCompile(`(?im)^name:\s*['"]?([^'"\r\n]+)`)

func skillName(path string, content []byte) string {
	if match := skillNameRe.FindSubmatch(content); len(match) == 2 {
		if name := strings.TrimSpace(string(match[1])); name != "" {
			return name
		}
	}
	base := filepath.Base(filepath.Dir(path))
	if strings.EqualFold(filepath.Base(path), "skill.md") && base != "." && base != "" {
		return base
	}
	return filepath.Base(path)
}
