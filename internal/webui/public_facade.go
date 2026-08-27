package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
)

const (
	publicAgentHeader   = "X-ASG-Agent-ID"
	publicTypeHeader    = "X-ASG-Agent-Type"
	publicSessionHeader = "X-ASG-Session"
)

// RegisterPublicLLM adds the config-only LLM facade. The upstream remains on
// the Gateway host; remote agents never need the local bridge binary.
func (s *Server) RegisterPublicLLM(mux *http.ServeMux, upstreamBase string) {
	mux.HandleFunc("/v1/", s.publicLLM)
	s.publicLLMUpstream = strings.TrimRight(upstreamBase, "/")
}

// WrapPublicMCP tracks the remote Agent before the shared MCP decision handler
// runs. It intentionally does not grant operator permissions.
func (s *Server) WrapPublicMCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.TrackPublicAgent(r, "mcp")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) publicLLM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.publicLLMUpstream == "" {
		http.Error(w, "LLM facade is not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	var requestMeta struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &requestMeta)

	agentID := publicAgentID(r)
	sessionID := r.Header.Get(publicSessionHeader)
	if sessionID == "" {
		sessionID = agentID + "-session"
	}
	s.TrackPublicAgent(r, requestMeta.Model)

	u, err := url.Parse(s.publicLLMUpstream + r.URL.Path)
	if err != nil {
		http.Error(w, "invalid LLM upstream", http.StatusInternalServerError)
		return
	}
	u.RawQuery = r.URL.RawQuery
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "create LLM upstream request", http.StatusInternalServerError)
		return
	}
	copyForwardHeaders(upReq.Header, r.Header)
	// The central bridge owns the provider credential. Never forward a remote
	// Agent's bearer/API key to the internal upstream.
	upReq.Header.Del("Authorization")
	upReq.Header.Del("X-Api-Key")
	upReq.Header.Set(publicAgentHeader, agentID)
	upReq.Header.Set(publicSessionHeader, sessionID)

	resp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		http.Error(w, "LLM upstream unavailable", http.StatusBadGateway)
		s.writePublicLLMEvent(agentID, sessionID, requestMeta.Model, "", http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	for k, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	var captured bytes.Buffer
	_, copyErr := io.Copy(w, io.TeeReader(resp.Body, &captured))
	observedModel := responseModel(captured.Bytes())
	if observedModel != "" {
		s.setPublicObservedModel(agentID, observedModel)
	}
	s.writePublicLLMEvent(agentID, sessionID, requestMeta.Model, observedModel, resp.StatusCode, copyErrString(copyErr))
}

func (s *Server) TrackPublicAgent(r *http.Request, model string) string {
	if s.Agents == nil {
		return publicAgentID(r)
	}
	now := time.Now().UTC()
	agentID := publicAgentID(r)
	sessionID := r.Header.Get(publicSessionHeader)
	if sessionID == "" {
		sessionID = agentID + "-session"
	}
	ip := requestIP(r)
	agentType := publicAgentType(r)
	alias := strings.TrimSpace(r.Header.Get("X-ASG-Agent-Alias"))
	if len(alias) > 128 {
		alias = alias[:128]
	}
	declaredModel := model
	effectiveModel := model
	var observedModel string
	if model == "mcp" {
		if old, ok := s.Agents.Get(agentID); ok {
			declaredModel = old.DeclaredModel
			effectiveModel = old.Model
			observedModel = old.ObservedModel
		}
	}
	rec := agentregistry.Record{
		AgentID: agentID, SessionID: sessionID, Alias: alias,
		AgentType: agentType, IP: ip, ConnectionIP: ip,
		Model: effectiveModel, DeclaredModel: declaredModel, ObservedModel: observedModel,
		Status: "online", Isolation: "active",
		RegisteredAt: now, LastHeartbeat: now, LastActivity: now,
	}
	if err := s.Agents.Upsert(rec); err != nil {
		return agentID
	}
	return agentID
}

func (s *Server) setPublicObservedModel(agentID, observed string) {
	if s.Agents == nil || observed == "" {
		return
	}
	rec, ok := s.Agents.Get(agentID)
	if !ok {
		return
	}
	rec.ObservedModel = observed
	rec.Model = observed
	_ = s.Agents.Upsert(rec)
}

func (s *Server) writePublicLLMEvent(agentID, sessionID, requested, observed string, status int, errText string) {
	if s.Store == nil {
		return
	}
	meta, _ := json.Marshal(map[string]any{
		"protocol": "anthropic", "status": status, "requested_model": requested,
		"observed_model": observed, "error": errText,
	})
	callID := fmt.Sprintf("public-llm-%d", time.Now().UnixNano())
	_ = s.Store.Write(api.Event{
		SessionID: sessionID,
		Call: api.ToolCall{
			CallID:    callID,
			Principal: api.Principal{AgentID: agentID, SessionID: sessionID, Role: "observer"},
			ToolID:    "llm.messages", Resource: "llm", Action: "inference",
			Arguments: meta, Timestamp: time.Now().UTC(),
		},
		Result: &api.ToolResult{CallID: callID, Output: meta},
		Decision: api.Decision{CallID: callID, Phase: api.PhasePost, Final: api.VerdictAllow,
			Rationale: "public config-only LLM facade"},
	})
}

func publicAgentID(r *http.Request) string {
	id := strings.TrimSpace(r.Header.Get(publicAgentHeader))
	if id == "" {
		// Without an explicit stable runtime ID, use the connection identity and
		// runtime type. Session IDs and model names are deliberately excluded.
		id = "public-" + identityPart(requestIP(r)) + "-" + identityPart(publicAgentType(r))
	}
	if len(id) > 128 {
		id = id[:128]
	}
	return id
}

func publicAgentType(r *http.Request) string {
	typ := strings.ToLower(strings.TrimSpace(r.Header.Get(publicTypeHeader)))
	if typ == "" {
		// If the caller sent a stable agent ID, trust we already know it's
		// an opencode runtime (our default local harness). Path alone cannot
		// distinguish opencode vs claude-code on /v1/messages.
		if strings.TrimSpace(r.Header.Get(publicAgentHeader)) != "" {
			typ = "opencode"
		} else {
			switch {
			case strings.HasSuffix(strings.ToLower(r.URL.Path), "/messages"):
				typ = "claude-code"
			case strings.Contains(strings.ToLower(r.URL.Path), "/chat/completions"):
				typ = "opencode"
			default:
				typ = "claude-code"
			}
		}
	}
	if len(typ) > 64 {
		typ = typ[:64]
	}
	return typ
}

func identityPart(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	value = strings.Trim(b.String(), "-.")
	if value == "" {
		return "unknown"
	}
	return value
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func responseModel(body []byte) string {
	var meta struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &meta) == nil {
		return meta.Model
	}
	return ""
}

func copyForwardHeaders(dst, src http.Header) {
	for k, values := range src {
		lk := strings.ToLower(k)
		switch lk {
		case "host", "content-length", "authorization", "x-api-key":
			continue
		}
		for _, value := range values {
			dst.Add(k, value)
		}
	}
}

func copyErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
