package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// apiAgentHistory returns model change history for an agent.
func (s *Server) apiAgentHistory(w http.ResponseWriter, r *http.Request) {
	if s.Agents == nil {
		http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
		return
	}
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}
	if _, ok := s.Agents.Get(agentID); !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	history, err := s.Agents.ModelHistory(agentID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"agent_id": agentID,
		"history":  history,
	})
}

// apiAgentDelete removes an agent manually (offline only via UI).
func (s *Server) apiAgentDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "DELETE or POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.Agents == nil {
		http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
		return
	}
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		// Try body
		var body struct {
			AgentID string `json:"agent_id"`
		}
		_ = parseJSONBody(r, &body)
		agentID = strings.TrimSpace(body.AgentID)
	}
	if agentID == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}
	if err := s.Agents.Delete(agentID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// Also clear activity chain
	if s.Activity != nil {
		s.Activity.Clear(agentID)
	}
	writeJSON(w, map[string]any{"ok": true, "deleted": agentID})
}

func parseJSONBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v)
}
