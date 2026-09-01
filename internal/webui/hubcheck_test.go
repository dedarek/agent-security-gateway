package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Hook PEP: /api/hub-check evaluates a tool call through the decision engine
// and returns ALLOW / BLOCK with a reason. Claude Code's PreToolUse hook
// POSTs here via the local probe.
func TestHubCheckAllowsSafeBash(t *testing.T) {
	s := New(nil, nil, nil)
	// no engine wired -> fail-open ALLOW (local rules still apply at probe)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hub-check", s.apiHubCheck)

	body := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls /home/server"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/hub-check", stringsReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("safe bash should be 200, got %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["decision"] != "allow" {
		t.Fatalf("expected allow, got %v", out)
	}
}

func TestHubCheckBlocksDangerousPattern(t *testing.T) {
	s := New(nil, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hub-check", s.apiHubCheck)

	body := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/hub-check", stringsReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("rm -rf / should be 403, got %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["decision"] != "block" {
		t.Fatalf("expected block, got %v", out)
	}
	if out["reason"] == "" {
		t.Fatal("block must carry a reason")
	}
}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }
