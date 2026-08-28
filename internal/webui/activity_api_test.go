package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/activity"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

func newActivityServer(t *testing.T) (*Server, *agentregistry.Registry, *activity.Store, *http.ServeMux) {
	t.Helper()
	st, _ := store.Open("")
	reg, _ := agentregistry.Open(filepath.Join(t.TempDir(), "agents.json"))
	_ = reg.Upsert(agentregistry.Record{AgentID: "test-agent", ProbeID: "probe-1", MachineID: "m-1", AgentType: "claude-code"})
	act := activity.New()
	s := New(st, nil, nil)
	s.SetAgentRegistry(reg)
	s.SetActivityStore(act)
	mux := http.NewServeMux()
	s.RegisterOTLP(mux)
	return s, reg, act, mux
}

func TestActivityHookClaudeCode(t *testing.T) {
	_, reg, act, mux := newActivityServer(t)
	body := `{"agent_id":"test-agent","agent_type":"claude-code","event":"tool_use","detail":"tool","hook_payload":{"tool_name":"Read","tool_input":{"file_path":"/tmp/foo.txt"},"session_id":"sess-1"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/activity", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	steps := act.List("test-agent")
	if len(steps) != 1 {
		t.Fatalf("steps=%v", steps)
	}
	if steps[0].ToolName != "Read" {
		t.Fatalf("tool=%q", steps[0].ToolName)
	}
	if steps[0].Summary != "file_path=/tmp/foo.txt" {
		t.Fatalf("summary=%q", steps[0].Summary)
	}
	if steps[0].SessionID != "sess-1" {
		t.Fatalf("session=%q", steps[0].SessionID)
	}
	// Registry should have advanced LastActivity and session
	got, _ := reg.Get("test-agent")
	if got.Status != "active" {
		t.Fatalf("status=%q", got.Status)
	}
	found := false
	for _, s := range got.SessionIDs {
		if s == "sess-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("session not attached: %v", got.SessionIDs)
	}
}

func TestActivityHookMinimalDetail(t *testing.T) {
	_, _, act, mux := newActivityServer(t)
	body := `{"agent_id":"test-agent","detail":"minimal","hook_payload":{"tool_name":"Bash","tool_input":{"command":"ls -la"},"session_id":"sess-2"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/activity", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(httptest.NewRecorder(), req)
	steps := act.List("test-agent")
	if len(steps) != 1 || steps[0].Summary != "" {
		t.Fatalf("minimal should crop summary, got %q", steps[0].Summary)
	}
}

func TestActivityUnregisteredIgnored(t *testing.T) {
	_, _, act, mux := newActivityServer(t)
	body := `{"agent_id":"ghost","hook_payload":{"tool_name":"Read"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/activity", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var m map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&m)
	if m["status"] != "ignored" {
		t.Fatalf("expected ignored, got %v", m)
	}
	if len(act.AllAgents()) != 0 {
		t.Fatalf("should not store for unregistered")
	}
}

func TestActivitySessionStart(t *testing.T) {
	_, _, act, mux := newActivityServer(t)
	body := `{"agent_id":"test-agent","event":"session_start","hook_payload":{"session_id":"sess-3"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/activity", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(httptest.NewRecorder(), req)
	steps := act.List("test-agent")
	if len(steps) != 1 || steps[0].Kind != "session_start" {
		t.Fatalf("steps=%v", steps)
	}
}

func TestActivityDetailChainInAgentDetail(t *testing.T) {
	st, _ := store.Open("")
	reg, _ := agentregistry.Open(filepath.Join(t.TempDir(), "agents.json"))
	_ = reg.Upsert(agentregistry.Record{AgentID: "test-agent", ProbeID: "p", MachineID: "m"})
	act := activity.New()
	act.Add(activity.Step{AgentID: "test-agent", ToolName: "Read", Summary: "file_path=/tmp/x", SessionID: "s1"})
	s := New(st, nil, nil)
	s.SetAgentRegistry(reg)
	s.SetActivityStore(act)
	req := httptest.NewRequest(http.MethodGet, "/api/agents/detail?agent_id=test-agent", nil)
	rec := httptest.NewRecorder()
	s.apiAgentDetail(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
	var out map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&out)
	chain, ok := out["chain"].([]any)
	if !ok || len(chain) != 1 {
		t.Fatalf("chain=%v", out["chain"])
	}
}
