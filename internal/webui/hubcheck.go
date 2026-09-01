package webui

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

// apiHubCheck is the Hook PEP endpoint. Claude Code's PreToolUse hook POSTs
// the tool call (via the local probe) here; the gateway runs it through the
// decision engine (Cedar / taint / DLP) and returns ALLOW / BLOCK with a
// reason. This is how tool EXECUTION gets enforcement (not just visibility).
//
//	POST /api/hub-check
//	{ "session_id": "...", "tool_name": "Bash", "tool_input": {...} }
//	→ 200 {"decision":"allow"} | 403 {"decision":"block","reason":"..."}
func (s *Server) apiHubCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		SessionID string          `json:"session_id"`
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 1. Deterministic local rules (dangerous patterns) — fast path, no engine.
	reason := localHookVerdict(payload.ToolName, payload.ToolInput)
	if reason != "" {
		s.reportHookTool(payload, "BLOCK", reason)
		writeBlock(w, reason)
		return
	}

	// 2. Decision engine (if wired): Cedar / taint / DLP.
	if s.Engine != nil {
		call := &api.ToolCall{
			CallID:    "hook-" + payload.ToolName,
			ToolID:    payload.ToolName,
			Arguments: payload.ToolInput,
			Principal: api.Principal{SessionID: payload.SessionID, AgentID: "local-claude-code"},
		}
		dec := s.Engine.EvaluatePre(r.Context(), call)
		if dec.Final == api.VerdictBlock {
			reason := "policy: " + firstReason(&dec)
			s.reportHookTool(payload, "BLOCK", reason)
			writeBlock(w, reason)
			return
		}
	}

	// 3. Allow + record for audit.
	s.reportHookTool(payload, "ALLOW", "")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"decision": "allow"})
}

func (s *Server) reportHookTool(payload struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}, verdict, reason string) {
	// Data lineage: record the data-flow hop for this tool call.
	if s.DataAccess != nil {
		s.DataAccess.ObserveHook(payload.SessionID, "hook-"+payload.ToolName, payload.ToolName, payload.ToolInput, verdict)
	}
	// Audit event (agent registry / store).
	if s.Store != nil {
		ev := api.Event{
			SessionID: payload.SessionID,
			Call: api.ToolCall{
				CallID:    "hook-" + payload.ToolName,
				Principal: api.Principal{SessionID: payload.SessionID, AgentID: "local-claude-code"},
				ToolID:    "hook." + payload.ToolName,
				Arguments: payload.ToolInput,
			},
			Decision: api.Decision{Final: verdictToEnum(verdict), Rationale: reason},
		}
		s.Store.Write(ev)
	}
}

func verdictToEnum(v string) api.Verdict {
	switch v {
	case "BLOCK":
		return api.VerdictBlock
	case "CONFIRM":
		return api.VerdictConfirm
	case "REDACT":
		return api.VerdictRedact
	default:
		return api.VerdictAllow
	}
}

func writeBlock(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]any{"decision": "block", "reason": reason})
}

func firstReason(d *api.Decision) string {
	if d == nil {
		return ""
	}
	if d.Rationale != "" {
		return d.Rationale
	}
	for _, s := range d.Signals {
		for _, r := range s.Reasons {
			if r != "" {
				return r
			}
		}
	}
	return d.Final.String()
}

// localHookVerdict returns a block reason if the tool call matches a
// deterministic dangerous pattern, else "".
func localHookVerdict(tool string, input json.RawMessage) string {
	s := strings.ToLower(string(input))
	blocked := []string{
		"rm -rf /", "drop table", "shutdown", ":(){", "mkfs",
		"attacker@gmail.com",
	}
	for _, b := range blocked {
		if strings.Contains(s, b) {
			return "local policy: dangerous pattern " + b
		}
	}
	return ""
}
