package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/db"
)

func TestInventoryIngestUpsertsObservationWithoutTrustingIt(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	s := New(nil, nil, nil)
	s.SetInventoryDB(database)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/inventory/ingest", s.apiInventoryIngest)

	payload := db.InventoryItem{
		AgentID:      "hand-rolled-agent",
		Kind:         "mcp_tool",
		Name:         "send_data",
		Origin:       "local-server",
		SchemaHash:   "sha256:v1",
		Status:       "trusted",
		RiskLevel:    "L2",
		RiskLabels:   []string{"external-network"},
		ManifestHash: "sha256:server-v1",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/inventory/ingest", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingest status = %d, body=%s", rec.Code, rec.Body.String())
	}

	items, err := db.ListInventory(database, "hand-rolled-agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one ingested item, got %d", len(items))
	}
	if items[0].Status != "pending_review" {
		t.Fatalf("ingest must not trust caller status, got %q", items[0].Status)
	}
	if items[0].AIAnalysisStatus != "pending" {
		t.Fatalf("ingest must queue analysis, got %q", items[0].AIAnalysisStatus)
	}
}
