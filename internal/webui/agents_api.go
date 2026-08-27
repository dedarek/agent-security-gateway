package webui

import (
	"net/http"
	"strings"
)

// apiAgents returns registered agents enriched with observed event statistics.
func (s *Server) apiAgents(w http.ResponseWriter, r *http.Request) {
	events := s.Store.Recent(1000)
	observed := map[string]*AgentInfo{}
	for _, e := range events {
		sid := e.SessionID
		p := e.Call.Principal
		id := p.AgentID
		if id == "" {
			id = sid
		}
		v := observed[sid]
		if v == nil {
			v = &AgentInfo{SessionID: sid, AgentID: id, UserID: p.UserID, Role: p.Role, LastVerdict: "ALLOW", Isolation: "active", SessionCount: 1}
			observed[sid] = v
		}
		if v.UserID == "" {
			v.UserID = p.UserID
		}
		v.EventCount++
		v.LastActivity = e.Timestamp.Format("2006-01-02 15:04:05")
		if e.Decision.Final.String() != "ALLOW" {
			v.LastVerdict = e.Decision.Final.String()
		}
	}

	out := []*AgentInfo{}
	if s.Agents != nil {
		for _, rec := range s.Agents.ListActive() {
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
			v.SessionCount = len(rec.SessionIDs)
			if v.SessionCount == 0 && sid != "" {
				v.SessionCount = 1
			}
			v.RegisteredAt = rec.RegisteredAt.Format("2006-01-02 15:04:05")
			v.LastHeartbeat = rec.LastHeartbeat.Format("2006-01-02 15:04:05")
			v.RestartCount = rec.RestartCount
			if v.LastActivity == "" {
				v.LastActivity = rec.LastActivity.Format("2006-01-02 15:04:05")
			}
			out = append(out, v)
		}
	}
	writeJSON(w, out)
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
