package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
)

// PostToolUse: observe-only — no decision, no block, just records the
// real execution result (tool_response) into taint/DataAccess.
func TestHubCheckPostToolUseObserves(t *testing.T) {
	s, reg := newTestServerWithRegistry(t)
	if err := reg.Upsert(agentregistry.Record{AgentID: "ag1", SessionID: "s1", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	// even in KILL mode, PostToolUse must NOT block (tool already ran)
	if _, err := reg.SetMode("ag1", agentregistry.ModeKill, "t"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hub-check", s.apiHubCheck)

	body := `{"session_id":"s1","agent_id":"ag1","tool_name":"Read","tool_input":{"file_path":"/repo/.env"},"tool_response":"API_KEY=sk-real","hook_event_name":"PostToolUse"}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hub-check?phase=post", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PostToolUse should be 200 observe (tool already ran), got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "observe") {
		t.Fatalf("expected observe decision, got %s", rec.Body.String())
	}
}

// PostToolUse with phase=post query param also works (probe adds it).
func TestHubCheckPostToolUsePhaseParam(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hub-check", s.apiHubCheck)
	body := `{"session_id":"s1","agent_id":"ag1","tool_name":"Bash","tool_input":{"command":"ls"},"tool_response":"file1"}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hub-check?phase=post", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("phase=post should observe, got %d", rec.Code)
	}
}
