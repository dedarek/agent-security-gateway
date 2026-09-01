package webui

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
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
		SessionID     string          `json:"session_id"`
		AgentID       string          `json:"agent_id"`
		ToolName      string          `json:"tool_name"`
		ToolInput     json.RawMessage `json:"tool_input"`
		ToolResponse  json.RawMessage `json:"tool_response"`
		HookEventName string          `json:"hook_event_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}

	// PostToolUse (phase=post): the tool already executed. No decision —
	// observe the real result so taint/DataAccess reflect actual execution.
	if r.URL.Query().Get("phase") == "post" || payload.HookEventName == "PostToolUse" {
		if s.Engine != nil {
			wrapped, _ := json.Marshal(map[string]any{
				"session_id":    payload.SessionID,
				"tool_name":     payload.ToolName,
				"tool_input":    payload.ToolInput,
				"tool_response": payload.ToolResponse,
			})
			s.Engine.ObserveHook(payload.SessionID, payload.ToolName, wrapped)
		}
		if s.DataAccess != nil {
			wrapped, _ := json.Marshal(map[string]any{
				"session_id":    payload.SessionID,
				"tool_name":     payload.ToolName,
				"tool_input":    payload.ToolInput,
				"tool_response": payload.ToolResponse,
			})
			s.DataAccess.ObserveHook(payload.SessionID, "hook-"+payload.ToolName, payload.ToolName, wrapped, "ALLOW")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"decision": "observe"})
		return
	}

	// 0. Administrative protection mode (Kill Switch) — takes precedence over
	// all policy rules. KILL denies everything; QUARANTINE allows reads and
	// local analysis only.
	if s.Agents != nil {
		mode := s.Agents.ModeOf(payload.AgentID)
		if mode != agentregistry.ModeNormal {
			transmits := isExternalSinkTool(payload.ToolName, payload.ToolInput)
			writes := isWriteTool(payload.ToolName, payload.ToolInput)
			destructive := localHookVerdict(payload.ToolName, payload.ToolInput) != ""
			if allow, why := mode.Allows(destructive, transmits, writes); !allow {
				s.reportHookTool(payload, "BLOCK", why)
				writeBlock(w, why)
				return
			}
		}
	}

	// 1. Deterministic local rules (dangerous patterns) — fast path, no engine.
	reason := localHookVerdict(payload.ToolName, payload.ToolInput)
	if reason != "" {
		s.reportHookTool(payload, "BLOCK", reason)
		writeBlock(w, reason)
		return
	}

	// 2. V0 session-level taint: if this session previously read sensitive
	// data (credential/secret taint) and this call transmits to an external
	// destination, BLOCK before it leaves the trust boundary.
	if s.Taints != nil && len(s.Taints(payload.SessionID)) > 0 && isExternalSinkTool(payload.ToolName, payload.ToolInput) {
		src := s.Taints(payload.SessionID)[0].Source
		reason := "DLP: session carries sensitive data (from " + src + ") and this call transmits externally"
		s.reportHookTool(payload, "BLOCK", reason)
		writeBlock(w, reason)
		return
	}
	// 3. Decision engine (if wired): Cedar / taint / DLP.
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
		// Observe the call so data-flow state (taint) accumulates for the
		// session — the cross-tool propagation primitive. EvaluatePre above
		// runs with this call's OWN provenance; ObserveHook records it.
		// Re-wrap tool_input so hookParts can parse it (it expects the
		// {tool_input: ...} envelope).
		hookPayload, _ := json.Marshal(map[string]any{
			"session_id": payload.SessionID,
			"tool_name":  payload.ToolName,
			"tool_input": payload.ToolInput,
		})
		s.Engine.ObserveHook(payload.SessionID, payload.ToolName, hookPayload)
	}

	// 3. Allow + record for audit.
	s.reportHookTool(payload, "ALLOW", "")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"decision": "allow"})
}

func (s *Server) reportHookTool(payload struct {
	SessionID     string          `json:"session_id"`
	AgentID       string          `json:"agent_id"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	HookEventName string          `json:"hook_event_name"`
}, verdict, reason string) {
	// Data lineage: record the data-flow hop for this tool call.
	if s.DataAccess != nil {
		// Re-wrap tool_input so the recorder's hookParts can extract
		// source/destination/trust zone correctly.
		wrapped, _ := json.Marshal(map[string]any{
			"session_id": payload.SessionID,
			"tool_name":  payload.ToolName,
			"tool_input": payload.ToolInput,
		})
		s.DataAccess.ObserveHook(payload.SessionID, "hook-"+payload.ToolName, payload.ToolName, wrapped, verdict)
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
		// Live KG ingest: hook-path events flow into the security graph so
		// the lineage view reflects activity within the request.
		if s.kgLive != nil {
			s.kgLive(ev)
		}
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

// isWriteTool reports whether a tool call mutates local state: Write/Edit
// tools, or shell commands with redirection/tee/mv/rm/cp.
func isWriteTool(tool string, input json.RawMessage) bool {
	name := strings.ToLower(tool)
	s := strings.ToLower(string(input))
	if strings.Contains(name, "write") || strings.Contains(name, "edit") ||
		strings.Contains(name, "patch") || strings.Contains(name, "create_file") {
		return true
	}
	// shell mutation patterns
	for _, p := range []string{">", ">>", "tee ", "mv ", "rm ", "cp ", "chmod ", "chown ", "mkdir ", "touch "} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// isExternalSinkTool reports whether a tool call transmits data to an
// external (non-local) destination: curl/wget/http/scp/ssh/nc pointing at a
// public host or IP. V0: conservative — any of these tools with a URL/IP is
// treated as an external sink.
func isExternalSinkTool(tool string, input json.RawMessage) bool {
	name := strings.ToLower(tool)
	s := strings.ToLower(string(input))
	// Generic exec tools (Bash/Write/Exec) are sinks when the COMMAND
	// contains an egress tool + URL/host.
	isGeneric := strings.Contains(name, "bash") || strings.Contains(name, "write") ||
		strings.Contains(name, "exec") || strings.Contains(name, "shell") ||
		strings.Contains(name, "run")
	if isGeneric {
		hasEgress := strings.Contains(s, "curl") || strings.Contains(s, "wget") ||
			strings.Contains(s, "scp") || strings.Contains(s, "ssh") ||
			strings.Contains(s, "nc ") || strings.Contains(s, "http://") ||
			strings.Contains(s, "https://")
		return hasEgress && (strings.Contains(s, "http://") || strings.Contains(s, "https://") ||
			strings.Contains(s, ".com") || strings.Contains(s, ".org") || strings.Contains(s, ".net") ||
			strings.Contains(s, ".io"))
	}
	// Dedicated network tools.
	isSinkTool := strings.Contains(name, "curl") || strings.Contains(name, "wget") ||
		strings.Contains(name, "http") || strings.Contains(name, "scp") ||
		strings.Contains(name, "ssh") || strings.Contains(name, "nc") ||
		strings.Contains(name, "send_email") || strings.Contains(name, "post") ||
		strings.Contains(name, "fetch") || strings.Contains(name, "web")
	if !isSinkTool {
		return false
	}
	if strings.Contains(s, "http://") || strings.Contains(s, "https://") {
		return true
	}
	for _, tok := range []string{".com", ".org", ".net", ".io", ".cn", ".ru"} {
		if strings.Contains(s, tok) && (strings.Contains(s, "curl") || strings.Contains(s, "wget")) {
			return true
		}
	}
	return false
}
