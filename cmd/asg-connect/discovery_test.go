package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/db"
)

func TestDiscoverLocalRootsFindsMCPConfigAndSkill(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agent-config.toml"), []byte(`[mcp_servers.time]
command = "uvx"
args = ["mcp-server-time"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, "skills", "clock")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: clock\n---\nTell time."), 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := discoverLocalRoots("agent-1", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected MCP server and skill, got %d: %#v", len(items), items)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Kind] = true
		if item.Status != "pending_review" || item.AIAnalysisStatus != "pending" {
			t.Fatalf("untrusted discovery state: %#v", item)
		}
	}
	if !seen["mcp_server"] || !seen["skill"] {
		t.Fatalf("missing normalized kinds: %#v", seen)
	}
}

func TestPostInventorySendsObservationOnly(t *testing.T) {
	var got db.InventoryItem
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/inventory/ingest" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	item := db.InventoryItem{AgentID: "agent-1", Kind: "skill", Name: "clock"}
	if err := postInventory(srv.Client(), srv.URL, item); err != nil {
		t.Fatal(err)
	}
	if got.AgentID != item.AgentID || got.Kind != item.Kind || got.Name != item.Name {
		t.Fatalf("observation was not forwarded: %#v", got)
	}
}
