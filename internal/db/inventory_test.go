package db

import (
	"testing"
)

func TestUpsertInventoryIsStableAndRequeuesChangedHash(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	item := InventoryItem{
		AgentID:          "local-codex",
		Kind:             "mcp_tool",
		Name:             "fetch",
		Source:           "config+protocol",
		Origin:           "uvx mcp-server-fetch",
		ManifestHash:     "sha256:server-v1",
		SchemaHash:       "sha256:tool-v1",
		Status:           "pending_review",
		RiskLevel:        "L2",
		AIAnalysisStatus: "pending",
	}

	first, err := UpsertInventory(database, item)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 {
		t.Fatal("expected persisted inventory id")
	}

	item.LastSeen = first.LastSeen + 1
	second, err := UpsertInventory(database, item)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("same capability created duplicate row: %d != %d", second.ID, first.ID)
	}
	items, err := ListInventory(database, "local-codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one inventory row, got %d", len(items))
	}

	item.SchemaHash = "sha256:tool-v2"
	item.Status = "trusted"
	item.AIAnalysisStatus = "completed"
	updated, err := UpsertInventory(database, item)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != first.ID {
		t.Fatalf("hash change created duplicate row: %d != %d", updated.ID, first.ID)
	}
	if updated.Status != "pending_review" {
		t.Fatalf("changed schema must return to pending_review, got %q", updated.Status)
	}
	if updated.AIAnalysisStatus != "pending" {
		t.Fatalf("changed schema must requeue analysis, got %q", updated.AIAnalysisStatus)
	}
}

func TestStableInventoryKeySeparatesAgentsAndKinds(t *testing.T) {
	one := StableInventoryKey("agent-a", "mcp_tool", "server", "fetch")
	two := StableInventoryKey("agent-b", "mcp_tool", "server", "fetch")
	three := StableInventoryKey("agent-a", "skill", "server", "fetch")
	if one == two || one == three {
		t.Fatal("inventory keys must separate agent and kind")
	}
}
