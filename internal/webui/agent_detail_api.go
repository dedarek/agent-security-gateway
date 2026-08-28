package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
)

var beijingDetail = time.FixedZone("CST", 8*3600)

var secretText = regexp.MustCompile(`(?i)(bearer\s+|api[_-]?key\s*[=:]\s*|token\s*[=:]\s*|password\s*[=:]\s*|secret\s*[=:]\s*)[^\s,;]+`)

// apiAgentDetail returns one registered identity plus its sessions and safe timeline.
// It merges two sources: engine events (store.Trajectory) and hook-driven
// activity chain (activity.Store). Timeline is engine-derived; chain is hook-derived.
func (s *Server) apiAgentDetail(w http.ResponseWriter, r *http.Request) {
	if s.Agents == nil {
		http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
		return
	}
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}
	rec, ok := s.Agents.Get(agentID)
	if !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	sessionIDs := append([]string{}, rec.SessionIDs...)
	if len(sessionIDs) == 0 && rec.SessionID != "" {
		sessionIDs = []string{rec.SessionID}
	}
	seen := map[string]bool{}
	var sessions []map[string]any
	var timeline []map[string]any
	for _, sid := range sessionIDs {
		if sid == "" || seen[sid] {
			continue
		}
		seen[sid] = true
		events := s.Store.Trajectory(sid)
		sessions = append(sessions, map[string]any{
			"session_id":    sid,
			"event_count":   len(events),
			"last_activity": lastEventTime(events),
		})
		for _, ev := range events {
			timeline = append(timeline, safeEvent(ev, rec.Model))
		}
	}
	// Activity chain from hooks (M1). Returned as separate key so UI can render
	// the detailed tool-use chain even when no engine events exist.
	var chain any
	if s.Activity != nil {
		steps := s.Activity.List(agentID)
		// Normalize times to Beijing strings for UI
		chainOut := make([]map[string]any, 0, len(steps))
		for _, st := range steps {
			chainOut = append(chainOut, map[string]any{
				"at":         st.At.In(beijingDetail).Format("2006-01-02 15:04:05"),
				"agent_id":   st.AgentID,
				"session_id": st.SessionID,
				"kind":       st.Kind,
				"tool":       st.ToolName,
				"summary":    st.Summary,
				"verdict":    st.Verdict,
			})
		}
		chain = chainOut
	}
	writeJSON(w, map[string]any{
		"agent":         rec,
		"sessions":      sessions,
		"timeline":      timeline,
		"chain":         chain,
		"timeline_note": "请求/响应默认安全截断并脱敏；历史模型需由 Agent 在事件中上报",
	})
}

func (s *Server) apiAgentAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.Agents == nil {
		http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		AgentID string `json:"agent_id"`
		Level   string `json:"level"`
		Alias   string `json:"alias"`
		Actor   string `json:"actor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.AgentID) == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}
	if req.Actor == "" {
		req.Actor = "operator"
	}
	var rec agentregistry.Record
	var err error
	if strings.TrimSpace(req.Alias) != "" {
		rec, err = s.Agents.SetAlias(req.AgentID, req.Alias, req.Actor)
	}
	if err == nil && strings.TrimSpace(req.Level) != "" {
		rec, err = s.Agents.SetIsolation(req.AgentID, strings.ToLower(strings.TrimSpace(req.Level)), req.Actor)
	}
	if strings.TrimSpace(req.Alias) == "" && strings.TrimSpace(req.Level) == "" {
		err = fmt.Errorf("level or alias is required")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "agent": rec})
}

func safeEvent(ev api.Event, model string) map[string]any {
	var sources []string
	for _, t := range ev.Call.Taints {
		if t.Source != "" {
			sources = append(sources, t.Source)
		}
	}
	return map[string]any{
		"call_id":       ev.Call.CallID,
		"timestamp":     ev.Timestamp.In(beijingDetail).Format("2006-01-02 15:04:05"),
		"session_id":    ev.SessionID,
		"trace_id":      ev.TraceID,
		"tool":          ev.Call.ToolID,
		"action":        ev.Call.Action,
		"verdict":       ev.Decision.Final.String(),
		"risk":          ev.Decision.Risk,
		"rationale":     safeText([]byte(ev.Decision.Rationale), 320),
		"request":       safeText(ev.Call.Arguments, 800),
		"response":      responseText(ev.Result),
		"input_sources": sources,
		"data_flow":     dataFlow(ev),
		"model":         model,
	}
}

func responseText(result *api.ToolResult) string {
	if result == nil {
		return ""
	}
	return safeText(result.Output, 800)
}

func dataFlow(ev api.Event) []string {
	out := make([]string, 0, len(ev.Call.Taints))
	for _, t := range ev.Call.Taints {
		if t.Source != "" {
			out = append(out, t.Source+" → "+ev.Call.ToolID)
		}
	}
	return out
}

func lastEventTime(events []api.Event) string {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].Timestamp.In(beijingDetail).Format("2006-01-02 15:04:05")
}

func safeText(b []byte, limit int) string {
	if len(b) == 0 {
		return ""
	}
	s := secretText.ReplaceAllString(string(b), "$1[REDACTED]")
	if len(s) > limit {
		s = s[:limit] + "…"
	}
	return s
}
