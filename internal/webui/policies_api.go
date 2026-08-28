package webui

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/dedarek/agent-security-gateway/internal/db"
)

var policiesDB *sql.DB

func SetPoliciesDB(d *sql.DB) { policiesDB = d }

// apiPolicies handles GET/PUT/DELETE for per-agent policies.
// GET  /api/policies?agent_id=<id>  -> list (agent-specific + global)
// PUT  /api/policies  {agent_id, axis, rule_id, action, enabled}
// DELETE /api/policies?id=<id>
func (s *Server) apiPolicies(w http.ResponseWriter, r *http.Request) {
	if policiesDB == nil {
		http.Error(w, "policies DB not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handlePoliciesList(w, r)
	case http.MethodPut, http.MethodPost:
		s.handlePoliciesUpsert(w, r)
	case http.MethodDelete:
		s.handlePoliciesDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePoliciesList(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if r.URL.Query().Get("all") == "true" || agentID == "" && r.URL.Query().Get("agent_id") == "" {
		// Check if ?all=true explicitly, otherwise if no agent_id, return all for admin
		if r.URL.Query().Get("agent_id") == "" {
			rows, err := db.ListAllPolicies(policiesDB)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if rows == nil {
				rows = []db.PolicyRow{}
			}
			writeJSON(w, rows)
			return
		}
	}
	rows, err := db.ListPolicies(policiesDB, agentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []db.PolicyRow{}
	}
	writeJSON(w, rows)
}

func (s *Server) handlePoliciesUpsert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID *string `json:"agent_id"`
		Axis    string  `json:"axis"`
		RuleID  string  `json:"rule_id"`
		Action  string  `json:"action"`
		Enabled *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.RuleID) == "" {
		http.Error(w, "rule_id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Axis) == "" {
		req.Axis = "permission"
	}
	if strings.TrimSpace(req.Action) == "" {
		req.Action = "block"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// AgentID: empty string or "global" means nil (global policy)
	var agentID *string
	if req.AgentID != nil {
		v := strings.TrimSpace(*req.AgentID)
		if v != "" && v != "global" {
			agentID = &v
		}
	}
	if err := db.UpsertPolicy(policiesDB, agentID, req.Axis, req.RuleID, req.Action, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handlePoliciesDelete(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		var body struct {
			ID int64 `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.ID != 0 {
			idStr = strconv.FormatInt(body.ID, 10)
		}
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id == 0 {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if err := db.DeletePolicy(policiesDB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "deleted": id})
}

func (s *Server) RegisterPerAgentPolicyAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/policies", s.Auth.middleware(s.apiPolicies))
}
