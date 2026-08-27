package webui

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/otlp"
)

// RegisterOTLP mounts the OTLP/HTTP trace endpoint. Standard exporters in
// OpenCode (experimental.openTelemetry), Claude Code, Codex, hermes-otel,
// pi-otel and OpenClaw diagnostics-otel all POST protobuf to /v1/traces.
// This is the telemetry channel: it never touches LLM traffic, so model
// switches and direct-connect providers stay fully visible.
func (s *Server) RegisterOTLP(mux *http.ServeMux) {
	mux.HandleFunc("/v1/traces", s.apiOTLPTraces)
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
