package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/activity"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/otlp"
)

// RegisterOTLP mounts the OTLP/HTTP trace + logs + metrics endpoints.
// Standard exporters in OpenCode (experimental.openTelemetry), Claude Code,
// Codex, hermes-otel, pi-otel and OpenClaw diagnostics-otel all POST protobuf
// to /v1/<signal>. This is the telemetry channel: it never touches LLM
// traffic, so model switches and direct-connect providers stay fully visible.
//
// Note: Claude Code's primary signals are METRICS and LOGS (api_request
// events carry model info); traces are a beta flag. We accept all three.
//
// We also expose a plain JSON /api/activity endpoint used by harnesses that
// have hook mechanisms (Claude Code PreToolUse/PostToolUse, OpenCode plugins,
// Codex notify scripts). It is a fallback for harnesses whose OTLP exporter
// is unreliable — hooks POST JSON via curl and never block the main loop.
func (s *Server) RegisterOTLP(mux *http.ServeMux) {
	mux.HandleFunc("/v1/traces", s.apiOTLPTraces)
	mux.HandleFunc("/v1/logs", s.apiOTLPLogs)
	mux.HandleFunc("/v1/metrics", s.apiOTLPMetrics)
	mux.HandleFunc("/api/activity", s.apiAgentActivity)
}

func (s *Server) apiOTLPTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	batches, err := otlp.DecodeExportTraceServiceRequest(body)
	if err != nil {
		http.Error(w, "bad OTLP payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if s.Agents == nil {
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}
	sig := otlp.SignalFromSpans(batches)
	ip := remoteHost(r.RemoteAddr)
	agentID := strings.TrimSpace(r.Header.Get(publicAgentHeader))
	if agentID == "" {
		agentID = otlpAgentIDFromResource(batches)
	}
	if agentID == "" {
		// Fallback IP+type only if that fallback row was already registered.
		typ := sig.AgentType
		if typ == "" {
			typ = "unknown"
		}
		candidate := "otel-" + sanitizeIDPart(ip) + "-" + sanitizeIDPart(typ)
		if _, ok := s.Agents.Get(candidate); ok {
			agentID = candidate
		}
	}
	if agentID == "" {
		// No stable ID and no registration — don't invent a row.
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		return
	}
	// Only registered agents are shown. OTLP just refreshes them.
	if _, ok := s.Agents.Get(agentID); !ok {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		return
	}
	now := time.Now().UTC()
	activity := sig.Latest
	if activity.IsZero() {
		activity = now
	}

	alias := strings.TrimSpace(r.Header.Get("X-ASG-Agent-Alias"))
	rec, exists := s.Agents.Get(agentID)
	if !exists {
		_ = s.Agents.Upsert(agentregistry.Record{
			AgentID: agentID, Alias: alias, AgentType: sig.AgentType,
			IP: ip, ConnectionIP: ip,
			Model: sig.Model, DeclaredModel: sig.Model,
			Status: "online", Isolation: "active",
			RegisteredAt: now, LastHeartbeat: now, LastActivity: activity,
		})
	} else {
		_ = s.Agents.Heartbeat(agentID, ip, nil, "", "", sig.AgentType, alias, activity)
	}
	if sig.Model != "" {
		_ = s.Agents.ObserveModel(agentID, sig.Model, "", activity)
	}
	if sig.SessionID != "" {
		_ = s.Agents.ObserveSession(agentID, sig.SessionID, activity)
	}
	_ = rec
	w.Header().Set("Content-Type", "application/x-protobuf")
	// ExportTraceServiceResponse is an empty message — zero bytes is valid.
	w.WriteHeader(http.StatusOK)
}

// apiAgentActivity is the generic activity beacon for harnesses with hook
// mechanisms (Claude Code PostToolUse, OpenCode plugin callbacks, Codex
// notify). Plain JSON POST, no protobuf. Body:
//   {"agent_id":"...","agent_type":"...","model":"...","session_id":"...","event":"tool_use","detail":"tool","hook_payload":{...}}
// All fields optional except agent_id. Registered agents only.
// hook_payload is parsed per-harness to extract tool name / session.
// The normalized Step is kept in the in-memory activity store and the
// agent's LastActivity is advanced.
func (s *Server) apiAgentActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.Agents == nil {
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}
	var body struct {
		AgentID     string          `json:"agent_id"`
		AgentType   string          `json:"agent_type"`
		Model       string          `json:"model"`
		SessionID   string          `json:"session_id"`
		Event       string          `json:"event"`
		Detail      string          `json:"detail"`
		HookPayload json.RawMessage `json:"hook_payload"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&body)
	agentID := strings.TrimSpace(body.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(r.Header.Get(publicAgentHeader))
	}
	if agentID == "" {
		writeJSON(w, map[string]string{"status": "ignored", "reason": "no agent_id"})
		return
	}
	if _, ok := s.Agents.Get(agentID); !ok {
		writeJSON(w, map[string]string{"status": "ignored", "reason": "not registered"})
		return
	}
	ip := remoteHost(r.RemoteAddr)
	now := time.Now().UTC()
	// Resolve session / tool from hook_payload if not already set.
	sessionID := strings.TrimSpace(body.SessionID)
	toolName, summary := parseHookPayload(body.HookPayload, body.Detail, &sessionID)
	kind := strings.TrimSpace(body.Event)
	if kind == "" {
		kind = "tool_use"
	}
	// Evaluate through engine (M3): if sensitive or policy blocks, we record verdict
	verdictStr := "ALLOW"
	var decision *api.Decision
	if s.Engine != nil && toolName != "" {
		// BUG-B fix: the hook path has no proxy in the data path, so
		// ResultObserver/ObserveResult never fires here and the taint axis was
		// dead for every hook-onboarded agent. Feed the raw hook payload to
		// hook-aware engines FIRST so this call's own provenance (source ingest
		// + derived artifacts) is visible to the decision below.
		s.Engine.ObserveHook(sessionID, toolName, body.HookPayload)
		call := api.ToolCall{
			CallID:    fmt.Sprintf("hook-%d", time.Now().UnixNano()),
			Principal: api.Principal{AgentID: agentID, SessionID: sessionID},
			ToolID:    toolName,
			Resource:  toolName,
			Action:    mapToolToAction(toolName),
			Arguments: body.HookPayload,
			Timestamp: now,
		}
		// For data-network taint, we need session context — use background with timeout inside engine
		d := s.Engine.EvaluatePre(context.Background(), &call)
		decision = &d
		verdictStr = d.Final.String()
		// Also evaluate post with empty result to catch output-side checks (stub)
		// Persist as event for audit/UI trajectory
		if s.Store != nil {
			ev := api.Event{
				SessionID: sessionID,
				Call:      call,
				Decision:  d,
				Timestamp: now,
			}
			if d.Final == api.VerdictBlock || d.Final == api.VerdictConfirm {
				ev.Result = &api.ToolResult{CallID: call.CallID, Output: []byte(verdictStr + ": " + d.Rationale)}
			}
			_ = s.Store.Write(ev)
			if s.kgLive != nil {
				s.kgLive(ev)
			}
		}
	}
	// Persist activity chain.
	if s.Activity != nil {
		step := activity.Step{
			At:        now,
			AgentID:   agentID,
			SessionID: sessionID,
			Kind:      kind,
			ToolName:  toolName,
			Summary:   summary,
			Verdict:   verdictStr,
			Raw:       body.HookPayload,
		}
		if decision != nil && decision.Rationale != "" {
			step.Reason = decision.Rationale
		}
		// Only store meaningful steps (kind/session/tool) or forced keepalive
		if toolName != "" || sessionID != "" || kind == "session_start" || kind == "session_end" {
			s.Activity.Add(step)
			s.NotifyActivity(step)
		} else if body.Model != "" || body.SessionID != "" {
			// Model/session ping without hook payload — still record as generic event
			s.Activity.Add(step)
			s.NotifyActivity(step)
		} else {
			// Pure keepalive — store minimal marker so the chain shows liveness,
			// but avoid spamming when hooks fire with empty payloads.
			if kind != "tool_use" {
				s.Activity.Add(step)
				s.NotifyActivity(step)
			}
		}
	}
	// If blocked, return blocking verdict so PreToolUse hook can enforce (non-zero exit)
	if decision != nil && decision.Final == api.VerdictBlock {
		// Still advance registry so activity shows
		if body.Model != "" {
			_ = s.Agents.ObserveModel(agentID, body.Model, "", now)
		}
		if sessionID != "" {
			_ = s.Agents.ObserveSession(agentID, sessionID, now)
		}
		_ = s.Agents.Heartbeat(agentID, ip, nil, "", "", body.AgentType, "", now)
		if rec, ok := s.Agents.Get(agentID); ok && rec.LastActivity.Before(now) {
			rec.LastActivity = now
			_ = s.Agents.Upsert(rec)
		}
		writeJSON(w, map[string]string{
			"status":  "blocked",
			"verdict": verdictStr,
			"code":    "SENSITIVE_OP_BLOCK",
			"message": decision.Rationale,
		})
		return
	}
	if decision != nil && decision.Final == api.VerdictConfirm {
		if body.Model != "" {
			_ = s.Agents.ObserveModel(agentID, body.Model, "", now)
		}
		if sessionID != "" {
			_ = s.Agents.ObserveSession(agentID, sessionID, now)
		}
		_ = s.Agents.Heartbeat(agentID, ip, nil, "", "", body.AgentType, "", now)
		if rec, ok := s.Agents.Get(agentID); ok && rec.LastActivity.Before(now) {
			rec.LastActivity = now
			_ = s.Agents.Upsert(rec)
		}
		writeJSON(w, map[string]string{
			"status":  "confirm",
			"verdict": verdictStr,
			"code":    "SENSITIVE_OP_CONFIRM",
			"message": decision.Rationale,
		})
		return
	}
	// Advance registry state.
	if body.Model != "" {
		_ = s.Agents.ObserveModel(agentID, body.Model, "", now)
	}
	if sessionID != "" {
		_ = s.Agents.ObserveSession(agentID, sessionID, now)
	}
	if body.Model == "" && sessionID == "" {
		_ = s.Agents.Heartbeat(agentID, ip, nil, "", "", body.AgentType, "", now)
		// Force LastActivity forward — Heartbeat doesn't do this anymore.
		if rec, ok := s.Agents.Get(agentID); ok && rec.LastActivity.Before(now) {
			rec.LastActivity = now
			_ = s.Agents.Upsert(rec)
		}
	} else {
		_ = s.Agents.Heartbeat(agentID, ip, nil, "", "", body.AgentType, "", now)
		// Ensure LastActivity is at least now (Observe* already set it when needed,
		// but pure hook events without model/session still need it).
		if rec, ok := s.Agents.Get(agentID); ok && rec.LastActivity.Before(now) {
			rec.LastActivity = now
			_ = s.Agents.Upsert(rec)
		}
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// parseHookPayload extracts tool/session from raw hook JSON. It handles
// Claude Code (tool_name/tool_input/session_id), OpenCode (tool/args/sessionID),
// and generic Codex shapes. detail controls summary cropping: minimal/tool/full.
func parseHookPayload(raw json.RawMessage, detail string, sessionID *string) (toolName, summary string) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return "", ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		// Not an object — treat as opaque summary
		s := string(raw)
		if len(s) > 800 {
			s = s[:800]
		}
		return "", s
	}
	// tool_name can be top-level or nested: try several keys
	for _, k := range []string{"tool_name", "tool", "toolName", "name"} {
		if v, ok := m[k]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && strings.TrimSpace(s) != "" {
				toolName = strings.TrimSpace(s)
				break
			}
		}
	}
	// session_id
	for _, k := range []string{"session_id", "sessionID", "sessionId", "sid"} {
		if v, ok := m[k]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && strings.TrimSpace(s) != "" && *sessionID == "" {
				*sessionID = strings.TrimSpace(s)
				break
			}
		}
	}
	// Build summary from tool_input / tool_input-like fields, respecting detail
	if detail == "minimal" {
		return toolName, ""
	}
	var inputRaw json.RawMessage
	for _, k := range []string{"tool_input", "toolInput", "input", "args", "arguments", "params"} {
		if v, ok := m[k]; ok {
			inputRaw = v
			break
		}
	}
	if len(inputRaw) != 0 && string(inputRaw) != "null" {
		// Try to produce a compact summary: key=value pairs, cropped
		var im map[string]any
		if json.Unmarshal(inputRaw, &im) == nil {
			// Common Claude Code fields: command / file_path / pattern / url
			for _, fk := range []string{"command", "file_path", "path", "pattern", "url", "prompt", "query", "content"} {
				if v, ok := im[fk]; ok {
					s := jsonOrString(v)
					if len(s) > 400 {
						s = s[:400] + "..."
					}
					summary = fk + "=" + s
					break
				}
			}
			if summary == "" {
				// Fallback: first value
				for _, v := range im {
					s := jsonOrString(v)
					if len(s) > 400 {
						s = s[:400] + "..."
					}
					summary = s
					break
				}
			}
		} else {
			var s string
			if json.Unmarshal(inputRaw, &s) == nil {
				summary = s
			} else {
				summary = string(inputRaw)
			}
			if len(summary) > 400 {
				summary = summary[:400] + "..."
			}
		}
	}
	if detail == "tool" && len(summary) > 200 {
		summary = summary[:200] + "..."
	}
	return toolName, summary
}

func jsonOrString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func mapToolToAction(tool string) string {
	switch tool {
	case "Read", "Grep", "Glob", "WebFetch", "WebSearch":
		return "read"
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		return "write"
	case "Bash", "Task", "ComputerUse":
		return "execute"
	default:
		return "read"
	}
}

// apiOTLPLogs accepts ExportLogsServiceRequest. Claude Code's primary
// signal; the api_request log event carries model name. We don't deep-parse
// the protobuf — the receipt itself is enough to mark the agent active.
func (s *Server) apiOTLPLogs(w http.ResponseWriter, r *http.Request) {
	s.apiOTLPGeneric(w, r, "logs")
}

// apiOTLPMetrics accepts ExportMetricsServiceRequest. Same shape as logs.
func (s *Server) apiOTLPMetrics(w http.ResponseWriter, r *http.Request) {
	s.apiOTLPGeneric(w, r, "metrics")
}

// apiOTLPGeneric is a lenient receiver for OTLP signals we don't fully
// decode yet: it ACKs the payload (so exporters don't retry-loop) and
// refreshes LastActivity on the matching registered agent.
func (s *Server) apiOTLPGeneric(w http.ResponseWriter, r *http.Request, kind string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	_ = body // future: parse model/session out of logs api_request events
	if s.Agents == nil {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		return
	}
	ip := remoteHost(r.RemoteAddr)
	agentID := strings.TrimSpace(r.Header.Get(publicAgentHeader))
	if agentID == "" {
		// No stable ID and no resource parse — try matching by source IP
		// against already-registered agents (cc-switch / claude-code with
		// proxy-managed tokens may not set service.instance.id).
		for _, rec := range s.Agents.List() {
			if rec.IP == ip || rec.ConnectionIP == ip {
				agentID = rec.AgentID
				break
			}
			for _, oip := range rec.ObservedIPs {
				if oip == ip {
					agentID = rec.AgentID
					break
				}
			}
			if agentID != "" {
				break
			}
		}
	}
	if agentID == "" {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, ok := s.Agents.Get(agentID); !ok {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		return
	}
	now := time.Now().UTC()
	_ = s.Agents.Heartbeat(agentID, ip, nil, "", "", "", "", now)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_ = kind
}

func remoteHost(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(remote)
}

func sanitizeIDPart(v string) string {
	var b strings.Builder
	for _, c := range v {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "unknown"
	}
	return out
}

func otlpAgentIDFromResource(batches []otlp.ResourceSpans) string {
	for _, b := range batches {
		for _, k := range []string{"service.instance.id", "asg.agent.id", "agent.id"} {
			if v := strings.TrimSpace(b.ResourceAttributes[k]); v != "" {
				return v
			}
		}
	}
	return ""
}
