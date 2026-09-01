package main

import (
	"encoding/json"
	"net/http"
)

// hookHTTPHandler handles POST /api/hook-check from Claude Code hooks.
// Input: Claude Code hook JSON payload on body
// Output: HTTP 200 = allow, HTTP 403 + reason = block
func hookHTTPHandler(cfg *ProbeConfig, rep *reporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		var payload struct {
			SessionID string          `json:"session_id"`
			ToolName  string          `json:"tool_name"`
			ToolInput json.RawMessage `json:"tool_input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		sessionID := payload.SessionID
		if sessionID == "" {
			sessionID = "hook-" + cfg.TenantName
		}

		// Local fast-path rules first.
		verdict, reason := localVerdict(payload.ToolName, payload.ToolInput)
		if verdict == "BLOCK" {
			rep.ReportTool(sessionID, "hook."+payload.ToolName, payload.ToolInput, verdict, reason)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"decision": "block", "reason": reason})
			return
		}

		// Defer to the hub decision engine (Cedar / taint / DLP) for the
		// real enforcement verdict — this is the Hook PEP data plane.
		// Fail-open: if the hub is unreachable, local rules already passed,
		// so allow (availability > enforcement); a BLOCK from the hub still
		// stops the tool.
		hubVerdict, hubReason, err := rep.hubCheck(r.Context(), sessionID, payload.ToolName, payload.ToolInput)
		if err == nil && hubVerdict == "BLOCK" {
			rep.ReportTool(sessionID, "hook."+payload.ToolName, payload.ToolInput, "BLOCK", hubReason)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"decision": "block", "reason": hubReason})
			return
		}

		rep.ReportTool(sessionID, "hook."+payload.ToolName, payload.ToolInput, "ALLOW", "")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"decision": "allow"})
	}
}
