package webui

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dedarek/agent-security-gateway/internal/db"
)

// apiInventoryIngest accepts an observation from a Probe. The caller cannot
// grant trust, lower risk, or install a policy through this endpoint.
func (s *Server) apiInventoryIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.InventoryDB == nil {
		http.Error(w, "inventory unavailable", http.StatusServiceUnavailable)
		return
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	var item db.InventoryItem
	if err := dec.Decode(&item); err != nil {
		http.Error(w, "invalid inventory observation", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(item.AgentID) == "" || strings.TrimSpace(item.Kind) == "" || strings.TrimSpace(item.Name) == "" {
		http.Error(w, "agent_id, kind and name are required", http.StatusBadRequest)
		return
	}

	// Ingest is an observation boundary, not an authorization boundary.
	item.Status = "pending_review"
	item.RiskLevel = ""
	item.RiskLabels = nil
	item.RiskReasons = nil
	item.PolicySuggestion = nil
	item.AIAnalysisStatus = "pending"
	stored, err := db.UpsertInventory(s.InventoryDB, item)
	if err != nil {
		http.Error(w, "inventory upsert failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{
		"accepted":   true,
		"id":         stored.ID,
		"stable_key": stored.StableKey,
		"status":     stored.Status,
	})
}

func (s *Server) apiInventory(w http.ResponseWriter, r *http.Request) {
	if s.InventoryDB == nil {
		http.Error(w, "inventory unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := db.ListInventory(s.InventoryDB, r.URL.Query().Get("agent_id"))
	if err != nil {
		http.Error(w, "inventory read failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}
