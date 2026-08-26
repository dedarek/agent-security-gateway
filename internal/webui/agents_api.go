package webui

import (
	"net/http"
)

// apiAgents returns aggregated agent connection info from events.
func (s *Server) apiAgents(w http.ResponseWriter, r *http.Request) {
	events := s.Store.Recent(1000)
	agents := map[string]*AgentInfo{}

	for _, e := range events {
		sid := e.SessionID
		p := e.Call.Principal
		// Use session_id as agent_id when Principal is empty (probe LLM events)
		agentID := p.AgentID
		if agentID == "" {
			agentID = sid // fallback to session id for probe events
		}
		userID := p.UserID
		if userID == "" {
			userID = sid // fallback
		}
		key := sid
		if _, ok := agents[key]; !ok {
			agents[key] = &AgentInfo{
				SessionID:   sid,
				UserID:      userID,
				AgentID:     agentID,
				Role:        p.Role,
				EventCount:  0,
				LastVerdict: "ALLOW",
			}
		}
		a := agents[key]
		a.EventCount++
		a.LastActivity = e.Timestamp.Format("2006-01-02 15:04:05")
		if e.Decision.Final.String() != "ALLOW" {
			a.LastVerdict = e.Decision.Final.String()
		}
	}

	out := []*AgentInfo{}
	for _, a := range agents {
		out = append(out, a)
	}
	writeJSON(w, out)
}

type AgentInfo struct {
	SessionID   string `json:"session_id"`
	UserID      string `json:"user_id"`
	AgentID     string `json:"agent_id"`
	Role        string `json:"role"`
	EventCount  int    `json:"event_count"`
	LastVerdict string `json:"last_verdict"`
	LastActivity string `json:"last_activity"`
}
