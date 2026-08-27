package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

func newPublicTestServer(t *testing.T) (*Server, *agentregistry.Registry) {
	t.Helper()
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := agentregistry.Open(t.TempDir() + "/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil)
	s.SetAgentRegistry(reg)
	return s, reg
}

func TestPublicLLMFacadeTracksAgentAndKeepsProviderCredentialInternal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("remote authorization leaked to internal upstream: %q", got)
		}
		if got := r.Header.Get("X-ASG-Agent-ID"); got != "agent-public-test" {
			t.Fatalf("agent id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"id\":\"msg_test\",\"model\":\"ox-alpha-free\",\"type\":\"message\",\"content\":[]}"))
	}))
	defer upstream.Close()

	s, reg := newPublicTestServer(t)
	mux := http.NewServeMux()
	s.RegisterPublicLLM(mux, upstream.URL)

	body := "{\"model\":\"claude-sonnet-4-6\",\"messages\":[],\"max_tokens\":8}"
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer remote-secret-must-not-forward")
	req.Header.Set("X-ASG-Agent-ID", "agent-public-test")
	req.Header.Set("X-ASG-Session", "session-public-test")
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "\"model\":\"ox-alpha-free\"") {
		t.Fatalf("response model missing: %s", resp.Body.String())
	}
	records := reg.List()
	if len(records) != 1 || records[0].AgentID != "agent-public-test" {
		t.Fatalf("registry = %#v", records)
	}
	if records[0].DeclaredModel != "claude-sonnet-4-6" || records[0].ObservedModel != "ox-alpha-free" {
		t.Fatalf("models = declared:%q observed:%q", records[0].DeclaredModel, records[0].ObservedModel)
	}
	var meta map[string]any
	if err := json.Unmarshal(storedEventArguments(s), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["requested_model"] != "claude-sonnet-4-6" || meta["observed_model"] != "ox-alpha-free" {
		t.Fatalf("event model metadata = %#v", meta)
	}
}

func storedEventArguments(s *Server) []byte {
	return s.Store.Recent(1)[0].Call.Arguments
}

func TestPublicMCPTrackingDoesNotOverwriteLLMModel(t *testing.T) {
	s, reg := newPublicTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req.Header.Set("X-ASG-Agent-ID", "agent-same")
	req.Header.Set("X-ASG-Session", "session-same")
	s.TrackPublicAgent(req, "claude-sonnet-4-6")
	s.setPublicObservedModel("agent-same", "ox-alpha-free")
	s.WrapPublicMCP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), req)
	rec, ok := reg.Get("agent-same")
	if !ok {
		t.Fatal("agent not tracked")
	}
	if rec.DeclaredModel != "claude-sonnet-4-6" || rec.ObservedModel != "ox-alpha-free" {
		t.Fatalf("models overwritten by MCP tracking: declared=%q observed=%q", rec.DeclaredModel, rec.ObservedModel)
	}
}

func TestPublicTrackingSeparatesRuntimeTypesButNotSessions(t *testing.T) {
	s, reg := newPublicTestServer(t)
	openCode := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	openCode.RemoteAddr = "203.0.113.9:1001"
	openCode.Header.Set("X-ASG-Session", "opencode-session-1")
	s.TrackPublicAgent(openCode, "ox-alpha-free")

	claude := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	claude.RemoteAddr = "203.0.113.9:1002"
	claude.Header.Set("X-ASG-Session", "claude-session-1")
	s.TrackPublicAgent(claude, "claude-sonnet-4-6")

	records := reg.List()
	if len(records) != 2 {
		t.Fatalf("runtime records = %#v", records)
	}
	if records[0].AgentType != "claude-code" || records[1].AgentType != "opencode" {
		t.Fatalf("runtime types = %#v", records)
	}
}
