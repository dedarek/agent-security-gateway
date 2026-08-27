package webui

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

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
//   {"agent_id":"...","agent_type":"...","model":"...","session_id":"...","event":"tool_use"}
// All fields optional except agent_id. Registered agents only.
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
		AgentID   string `json:"agent_id"`
		AgentType string `json:"agent_type"`
		Model     string `json:"model"`
		SessionID string `json:"session_id"`
		Event     string `json:"event"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
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
	// Hook-driven activity is real activity: advance LastActivity so the
	// 5-minute online window actually reflects hook events, not just LLM/OTLP.
	if body.Model != "" {
		_ = s.Agents.ObserveModel(agentID, body.Model, "", now)
	}
	if body.SessionID != "" {
		_ = s.Agents.ObserveSession(agentID, body.SessionID, now)
	}
	// If hook carried no model/session (pure ping), still mark active.
	if body.Model == "" && body.SessionID == "" {
		_ = s.Agents.Heartbeat(agentID, ip, nil, "", "", body.AgentType, "", now)
		// Force LastActivity forward — Heartbeat doesn't do this anymore.
		if rec, ok := s.Agents.Get(agentID); ok && rec.LastActivity.Before(now) {
			rec.LastActivity = now
			_ = s.Agents.Upsert(rec)
		}
	} else {
		_ = s.Agents.Heartbeat(agentID, ip, nil, "", "", body.AgentType, "", now)
	}
	writeJSON(w, map[string]string{"status": "ok"})
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
