// Hub-side ingest: receives probe event batches (NDJSON) and forwards them
// into the central event store; also serves remote CONFIRM checks from probes.
package webui

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

func (s *Server) RegisterIngest(mux *http.ServeMux) {
	s.RegisterIngestWithAuth(mux, s.ingestAuth)
}

// RegisterIngestWithAuth enforces tenant key auth on /api/ingest.
// auth==nil means open (dev mode); otherwise a request without a valid key gets 401.
func (s *Server) RegisterIngestWithAuth(mux *http.ServeMux, auth func(header string) bool) {
	mux.HandleFunc("/api/ingest", func(w http.ResponseWriter, r *http.Request) {
		if auth := s.effectiveIngestAuth(auth); auth != nil && !auth(r.Header.Get("Authorization")) {
			http.Error(w, "unauthorized", 401)
			return
		}
		s.apiIngest(w, r)
	})
}

func (s *Server) apiIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		if s.agentIngressOpen {
			if agentID, _ := raw["agent_id"].(string); strings.TrimSpace(agentID) == "" {
				continue
			}
		}
		ev := s.normalizeIngressEvent(raw)
		if s.Agents != nil && strings.HasPrefix(ev.Call.ToolID, "llm.") && ev.Call.Principal.AgentID != "" {
			_ = s.Agents.ObserveModel(ev.Call.Principal.AgentID, strings.TrimPrefix(ev.Call.ToolID, "llm."), "", ev.Timestamp)
		}
		if s.Agents != nil && ev.Call.Principal.AgentID != "" && ev.SessionID != "" {
			_ = s.Agents.ObserveSession(ev.Call.Principal.AgentID, ev.SessionID, ev.Timestamp)
		}
		s.Store.Write(ev)
		n++
	}
	writeJSON(w, map[string]int{"accepted": n})
}

// normalizeIngressEvent separates public telemetry identity from the
// client-supplied tenant and role fields. AgentID remains available for
// Registry correlation, but it does not grant permissions.
func (s *Server) normalizeIngressEvent(raw map[string]any) api.Event {
	if !s.agentIngressOpen {
		return normalizeProbeEvent(raw)
	}
	public := make(map[string]any, len(raw)+3)
	for k, v := range raw {
		public[k] = v
	}
	public["tenant_name"] = "public-ingress"
	public["principal"] = "public-ingress"
	public["role"] = "observer"
	return normalizeProbeEvent(public)
}

// normalizeProbeEvent maps a probe record into the shared Event schema.
// llm_call events are recorded as a synthetic session entry so the explorer
// can replay agent reasoning alongside tool calls.
func normalizeProbeEvent(raw map[string]any) api.Event {
	session, _ := raw["session"].(string)
	if session == "" {
		session = "probe-unknown"
	}
	kind, _ := raw["kind"].(string)
	ev := api.Event{
		SessionID: session,
		Timestamp: timeNow(),
	}
	if t, ok := raw["trace_id"].(string); ok {
		ev.TraceID = t
	}
	if p, ok := raw["parent"].(string); ok {
		ev.ParentID = p
	}
	tenantName, _ := raw["tenant_name"].(string)
	agentID, _ := raw["agent_id"].(string)
	if agentID == "" {
		agentID = tenantName
	}
	principal, _ := raw["principal"].(string)
	role, _ := raw["role"].(string)
	switch kind {
	case "llm_call":
		model, _ := raw["model"].(string)
		ev.Call = api.ToolCall{
			CallID:    idFor(session, "llm"),
			ToolID:    "llm." + model,
			Action:    "read",
			Arguments: mustJSONField(raw["request"]),
			Principal: api.Principal{
				UserID:    principal,
				AgentID:   agentID,
				SessionID: session,
				Role:      role,
			},
		}
		if resp := mustJSONField(raw["response"]); len(resp) > 0 {
			trunc := resp
			if len(trunc) > 16*1024 {
				trunc = trunc[:16*1024]
			}
			ev.Result = &api.ToolResult{Output: trunc}
		}
	case "tool_call":
		tool, _ := raw["tool"].(string)
		verdictStr, _ := raw["verdict"].(string)
		reason, _ := raw["reason"].(string)
		ev.Call = api.ToolCall{
			CallID:    idFor(session, tool),
			ToolID:    tool,
			Action:    "write",
			Arguments: mustJSONField(raw["args"]),
			Principal: api.Principal{
				UserID:    principal,
				AgentID:   agentID,
				SessionID: session,
				Role:      role,
			},
		}
		ev.Decision = decisionFromVerdict(verdictStr, reason)
	default:
		ev.Call = api.ToolCall{CallID: idFor(session, kind), ToolID: "probe." + kind}
	}
	return ev
}
