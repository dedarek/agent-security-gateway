package webui

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
)

var beijing = time.FixedZone("CST", 8*3600)

func fmtBeijing(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(beijing).Format("2006-01-02 15:04:05")
}

// apiAgents returns registered agents enriched with observed event statistics.
func (s *Server) apiAgentMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agentID := r.URL.Query().Get("agent_id")
		if agentID == "" {
			http.Error(w, "agent_id required", http.StatusBadRequest)
			return
		}
		mode := s.Agents.ModeOf(agentID)
		json.NewEncoder(w).Encode(map[string]any{"agent_id": agentID, "mode": mode})
	case http.MethodPost:
		var req struct {
			AgentID string `json:"agent_id"`
			Mode    string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.AgentID == "" || !agentregistry.IsValidMode(req.Mode) {
			http.Error(w, "agent_id + valid mode (normal/quarantine/kill) required", http.StatusBadRequest)
			return
		}
		actor := "operator"
		rec, err := s.Agents.SetMode(req.AgentID, agentregistry.ProtectionMode(req.Mode), actor)
		if err != nil {
			http.Error(w, "set mode failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// audit event
		if s.Store != nil {
			s.Store.Write(api.Event{
				SessionID: "console",
				Call: api.ToolCall{
					CallID:    "agent.mode_change",
					ToolID:    "agent.mode_change",
					Principal: api.Principal{AgentID: req.AgentID, SessionID: "console"},
					Arguments: json.RawMessage(`{"mode":"` + req.Mode + `","actor":"` + actor + `"}`),
				},
				Decision: api.Decision{Final: api.VerdictAllow, Rationale: "protection mode changed to " + req.Mode},
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"agent_id": rec.AgentID, "mode": rec.ProtectionMode, "changed_by": actor})
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// apiAgents returns registered agents enriched with observed event statistics.
func (s *Server) apiAgents(w http.ResponseWriter, r *http.Request) {
	events := s.Store.Recent(1000)
	observed := map[string]*AgentInfo{}
	observedSessions := map[string]map[string]bool{}
	for _, e := range events {
		sid := e.SessionID
		p := e.Call.Principal
		id := p.AgentID
		if id == "" {
			id = sid
		}
		v := observed[id]
		if v == nil {
			v = &AgentInfo{SessionID: sid, AgentID: id, UserID: p.UserID, Role: p.Role, LastVerdict: "ALLOW", Isolation: "active"}
			observed[id] = v
		}
		if observedSessions[id] == nil {
			observedSessions[id] = map[string]bool{}
		}
		if sid != "" {
			observedSessions[id][sid] = true
		}
		if v.UserID == "" {
			v.UserID = p.UserID
		}
		v.EventCount++
		v.LastActivity = fmtBeijing(e.Timestamp)
		if strings.HasPrefix(e.Call.ToolID, "llm.") {
			v.Model = strings.TrimPrefix(e.Call.ToolID, "llm.")
		}
		if e.Decision.Final.String() != "ALLOW" {
			v.LastVerdict = e.Decision.Final.String()
		}
	}

	out := []*AgentInfo{}
	if s.Agents != nil {
		for _, rec := range s.Agents.List() {
			// Only registered agents (probe-backed) are shown.
			if rec.ProbeID == "" && rec.MachineID == "" {
				continue
			}
			sid := rec.SessionID
			if sid == "" {
				sid = rec.AgentID
			}
			v := observed[sid]
			if v == nil {
				v = &AgentInfo{SessionID: sid, AgentID: rec.AgentID, LastVerdict: "ALLOW", Isolation: "active"}
			}
			v.AgentID = rec.AgentID
			v.ProbeID = rec.ProbeID
			v.MachineID = rec.MachineID
			v.MachineName = rec.MachineName
			v.Alias = rec.Alias
			if strings.ContainsRune(v.Alias, '\ufffd') {
				v.Alias = "未命名 Agent"
			}
			v.AgentType = rec.AgentType
			v.ProcessID = rec.ProcessID
			v.OS = rec.OS
			v.User = rec.User
			v.IP = rec.IP
			v.DeclaredIPs = append([]string{}, rec.DeclaredIPs...)
			v.ObservedIPs = append([]string{}, rec.ObservedIPs...)
			v.ConnectionIP = rec.ConnectionIP
			v.Model = rec.Model
			v.Provider = rec.Provider
			v.Status = rec.Status
			v.Isolation = rec.Isolation
			v.SessionIDs = append([]string{}, rec.SessionIDs...)
			for sessionID := range observedSessions[rec.AgentID] {
				v.SessionIDs = appendUniqueString(v.SessionIDs, sessionID)
			}
			// The original registration heartbeat used AgentID as a placeholder
			// session. Once real telemetry sessions exist, hide that placeholder.
			if len(observedSessions[rec.AgentID]) > 0 {
				v.SessionIDs = removeString(v.SessionIDs, rec.AgentID)
			}
			v.SessionCount = len(v.SessionIDs)
			if v.SessionCount == 0 && sid != "" {
				v.SessionCount = 1
			}
			v.RegisteredAt = fmtBeijing(rec.RegisteredAt)
			v.LastHeartbeat = fmtBeijing(rec.LastHeartbeat)
			v.RestartCount = rec.RestartCount
			if v.LastActivity == "" {
				v.LastActivity = fmtBeijing(rec.LastActivity)
			}
			out = append(out, v)
		}
	}
	writeJSON(w, out)
}

func appendUniqueString(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func removeString(values []string, target string) []string {
	out := values[:0]
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

type AgentInfo struct {
	SessionID     string   `json:"session_id"`
	AgentID       string   `json:"agent_id"`
	UserID        string   `json:"user_id,omitempty"`
	Role          string   `json:"role,omitempty"`
	ProbeID       string   `json:"probe_id,omitempty"`
	MachineID     string   `json:"machine_id,omitempty"`
	MachineName   string   `json:"machine_name,omitempty"`
	Alias         string   `json:"alias,omitempty"`
	AgentType     string   `json:"agent_type,omitempty"`
	ProcessID     int      `json:"process_id,omitempty"`
	OS            string   `json:"os,omitempty"`
	User          string   `json:"user,omitempty"`
	IP            string   `json:"ip,omitempty"`
	DeclaredIPs   []string `json:"declared_ips,omitempty"`
	ObservedIPs   []string `json:"observed_ips,omitempty"`
	ConnectionIP  string   `json:"connection_ip,omitempty"`
	Model         string   `json:"model,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Status        string   `json:"status,omitempty"`
	Isolation     string   `json:"isolation"`
	SessionIDs    []string `json:"session_ids,omitempty"`
	SessionCount  int      `json:"session_count"`
	EventCount    int      `json:"event_count"`
	LastVerdict   string   `json:"last_verdict"`
	LastActivity  string   `json:"last_activity,omitempty"`
	RegisteredAt  string   `json:"registered_at,omitempty"`
	LastHeartbeat string   `json:"last_heartbeat,omitempty"`
	RestartCount  int      `json:"restart_count"`
}
