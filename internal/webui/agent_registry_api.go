package webui

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
)

func (s *Server) apiAgentRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.agentIngressOpen && s.ingestAuth != nil && !s.ingestAuth(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Agents == nil {
		http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
		return
	}
	var rec agentregistry.Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil || rec.AgentID == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}
	if rec.IP == "" {
		rec.IP = agentregistry.RemoteIP(r.RemoteAddr)
	}
	if rec.ConnectionIP == "" {
		rec.ConnectionIP = agentregistry.RemoteIP(r.RemoteAddr)
	}
	if s.agentIngressOpen {
		// Public registration cannot alter an existing lifecycle state.
		rec.Isolation = ""
	}
	if len(rec.ObservedIPs) == 0 && rec.IP != "" {
		rec.ObservedIPs = []string{rec.IP}
	}
	if err := s.Agents.Upsert(rec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "agent_id": rec.AgentID})
}

func (s *Server) apiAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.agentIngressOpen && s.ingestAuth != nil && !s.ingestAuth(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Agents == nil {
		http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		AgentID     string    `json:"agent_id"`
		IP          string    `json:"ip"`
		MachineName string    `json:"machine_name"`
		ObservedIPs []string  `json:"observed_ips"`
		Model       string    `json:"model"`
		Provider    string    `json:"provider"`
		AgentType   string    `json:"agent_type"`
		Alias       string    `json:"alias"`
		Activity    time.Time `json:"activity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.AgentID == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}
	// A heartbeat from the bridge is still useful identity evidence even when
	// the harness cannot report its own address (for example, Codex ChatGPT
	// login). Preserve the actual HTTP peer rather than leaving IP blank.
	if in.IP == "" {
		in.IP = agentregistry.RemoteIP(r.RemoteAddr)
	}
	if len(in.ObservedIPs) == 0 && in.IP != "" {
		in.ObservedIPs = []string{in.IP}
	}
	if err := s.Agents.Heartbeat(in.AgentID, in.IP, in.ObservedIPs, in.Model, in.Provider, in.AgentType, in.Alias, in.Activity); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Agents.UpdateMachine(in.AgentID, in.MachineName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
