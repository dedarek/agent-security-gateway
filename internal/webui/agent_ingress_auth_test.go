package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

func TestPublicAgentIngressAcceptsRegistrationWithoutBearer(t *testing.T) {
	registry, err := agentregistry.Open(t.TempDir() + "/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(nil, nil, nil)
	s.SetAgentRegistry(registry)
	s.SetIngestAuth(func(string) bool { return false })
	s.SetAgentIngressOpen(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents/register", s.apiAgentRegister)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/register", strings.NewReader(`{"agent_id":"public-agent-1","alias":"Public Agent"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected public registration to be accepted without bearer, got %d: %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, rec := range registry.List() {
		if rec.AgentID == "public-agent-1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("public agent was not written to registry")
	}
}

func TestPublicAgentIngressDoesNotTrustEventTenantOrRole(t *testing.T) {
	s := New(nil, nil, nil)
	s.SetAgentIngressOpen(true)

	raw := map[string]any{
		"kind":        "llm_call",
		"session":     "public-session",
		"agent_id":    "public-agent-1",
		"tenant_name": "forged-admin-tenant",
		"principal":   "forged-admin",
		"role":        "admin",
		"model":       "test-model",
		"request":     map[string]any{"prompt": "hello"},
	}
	ev := s.normalizeIngressEvent(raw)
	if ev.Call.Principal.UserID != "public-ingress" || ev.Call.Principal.Role != "observer" {
		t.Fatalf("public event trusted caller identity: user=%q role=%q", ev.Call.Principal.UserID, ev.Call.Principal.Role)
	}
	if ev.Call.Principal.AgentID != "public-agent-1" {
		t.Fatalf("agent id was not preserved: %q", ev.Call.Principal.AgentID)
	}
}

func TestPublicAgentIngressAcceptsTelemetryButKeepsOperatorAuth(t *testing.T) {
	evStore, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := agentregistry.Open(t.TempDir() + "/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(evStore, nil, nil)
	s.SetAgentRegistry(registry)
	s.SetIngestAuth(func(string) bool { return false })
	s.SetAgentIngressOpen(true)

	mux := http.NewServeMux()
	s.Register(mux)
	telemetry := `{"kind":"llm_call","session":"public-session","agent_id":"public-agent-1","tenant_name":"forged","principal":"forged","role":"admin","model":"test-model","request":{"prompt":"hello"},"response":{"content":"ok"}}` + "\n"
	req := httptest.NewRequest(http.MethodPost, "/api/ingest", strings.NewReader(telemetry))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"accepted":1`) {
		t.Fatalf("expected keyless telemetry to be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
	recent := evStore.Recent(1)
	if len(recent) != 1 || recent[0].Call.Principal.Role != "observer" {
		t.Fatalf("public telemetry identity was not downgraded: %+v", recent)
	}

	// Read-only operator GETs are intentionally public (免密只读公网): a remote
	// viewer may read the agent list without a session.
	adminReq := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	adminReq.RemoteAddr = "203.0.113.5:1234"
	adminRec := httptest.NewRecorder()
	mux.ServeHTTP(adminRec, adminReq)
	if adminRec.Code == http.StatusUnauthorized {
		t.Fatalf("read-only /api/agents should be public, got %d", adminRec.Code)
	}

	// Mutations and non-GET on operator APIs still require the admin session.
	writeReq := httptest.NewRequest(http.MethodDelete, "/api/agents/delete?agent_id=public-agent-1", nil)
	writeReq.RemoteAddr = "203.0.113.5:1234"
	writeRec := httptest.NewRecorder()
	mux.ServeHTTP(writeRec, writeReq)
	if writeRec.Code != http.StatusUnauthorized {
		t.Fatalf("remote mutation must stay protected, got %d", writeRec.Code)
	}
}
