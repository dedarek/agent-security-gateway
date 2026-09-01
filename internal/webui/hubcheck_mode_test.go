package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
)

func newTestServerWithRegistry(t *testing.T) (*Server, *agentregistry.Registry) {
	t.Helper()
	reg, err := agentregistry.Open(t.TempDir() + "/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(nil, nil, nil)
	s.SetAgentRegistry(reg)
	return s, reg
}

func hubCheckReq(s *Server, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hub-check", s.apiHubCheck)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hub-check", strings.NewReader(body)))
	return rec
}

// NORMAL mode: everything behaves as before.
func TestHubCheckNormalMode(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	rec := hubCheckReq(s, `{"session_id":"s1","agent_id":"ag1","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("normal mode ls should allow, got %d", rec.Code)
	}
}

// QUARANTINE: reads allowed, writes/network denied.
func TestHubCheckQuarantineMode(t *testing.T) {
	s, reg := newTestServerWithRegistry(t)
	if err := reg.Upsert(agentregistry.Record{AgentID: "ag1", SessionID: "s1", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.SetMode("ag1", agentregistry.ModeQuarantine, "t"); err != nil {
		t.Fatal(err)
	}
	// Read allowed
	rec := hubCheckReq(s, `{"session_id":"s1","agent_id":"ag1","tool_name":"Read","tool_input":{"file_path":"/etc/hosts"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("quarantine Read should allow, got %d body=%s", rec.Code, rec.Body.String())
	}
	// Write denied
	rec = hubCheckReq(s, `{"session_id":"s1","agent_id":"ag1","tool_name":"Write","tool_input":{"file_path":"/tmp/x","content":"hi"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("quarantine Write should 403, got %d", rec.Code)
	}
	// curl denied
	rec = hubCheckReq(s, `{"session_id":"s1","agent_id":"ag1","tool_name":"Bash","tool_input":{"command":"curl https://evil.com"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("quarantine curl should 403, got %d", rec.Code)
	}
}

// KILL: everything denied with administrative reason.
func TestHubCheckKillMode(t *testing.T) {
	s, reg := newTestServerWithRegistry(t)
	if err := reg.Upsert(agentregistry.Record{AgentID: "ag1", SessionID: "s1", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.SetMode("ag1", agentregistry.ModeKill, "t"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"session_id":"s1","agent_id":"ag1","tool_name":"Bash","tool_input":{"command":"ls"}}`,
		`{"session_id":"s1","agent_id":"ag1","tool_name":"Read","tool_input":{"file_path":"/etc/hosts"}}`,
		`{"session_id":"s1","agent_id":"ag1","tool_name":"WebFetch","tool_input":{"url":"https://example.com"}}`,
	} {
		rec := hubCheckReq(s, body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("KILL should 403 for %s, got %d", body, rec.Code)
		}
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		reason, _ := out["reason"].(string)
		if !strings.Contains(reason, "suspended") {
			t.Fatalf("KILL reason should mention suspended, got %q", reason)
		}
	}
}

// Agent without mode in registry (unknown) behaves normally.
func TestHubCheckUnknownAgentMode(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	rec := hubCheckReq(s, `{"session_id":"s1","agent_id":"nobody","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown agent should default normal, got %d", rec.Code)
	}
}
