package inventory

import (
	"testing"
)

func TestDiscoverMCPJSONNormalizesDifferentServerEntries(t *testing.T) {
	raw := []byte(`{
		"mcpServers": {
			"time": {"command": "uvx", "args": ["mcp-server-time"]},
			"docs": {"type": "http", "url": "https://example.test/mcp"}
		}
	}`)

	items, err := DiscoverMCPJSON("local-agent", "C:/agent/config.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(items))
	}
	if items[0].Kind != "mcp_server" || items[0].AgentID != "local-agent" {
		t.Fatalf("unexpected first item: %#v", items[0])
	}
	if items[0].Status != "pending_review" || items[0].AIAnalysisStatus != "pending" {
		t.Fatalf("new discovery must be pending: %#v", items[0])
	}
	if items[0].Origin == "" || items[0].ManifestHash == "" {
		t.Fatalf("discovery must retain origin and hash: %#v", items[0])
	}
	if items[0].StableKey == items[1].StableKey {
		t.Fatal("different servers must have different stable keys")
	}
}

func TestDiscoverConfigFileParsesTOMLWithoutHarnessBranch(t *testing.T) {
	raw := []byte(`[mcp_servers.time]
command = "uvx"
args = ["mcp-server-time"]
`)
	items, err := DiscoverConfigFile("local-agent", "C:/agent/config.toml", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "time" || items[0].Kind != "mcp_server" {
		t.Fatalf("unexpected TOML discovery: %#v", items)
	}
}

func TestDiscoverSkillFileCreatesContentHashedPendingItem(t *testing.T) {
	content := []byte("---\nname: release-helper\n---\nRun the release checks.")
	item := DiscoverSkillFile("local-agent", "C:/skills/release/SKILL.md", content)
	if item.Kind != "skill" || item.Name != "release-helper" {
		t.Fatalf("unexpected skill item: %#v", item)
	}
	if item.Status != "pending_review" || item.AIAnalysisStatus != "pending" {
		t.Fatalf("skill must start pending: %#v", item)
	}
	if item.ManifestHash == "" || item.InstallPath == "" {
		t.Fatalf("skill must be content-addressed: %#v", item)
	}
}
