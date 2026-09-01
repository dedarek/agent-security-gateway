package webui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

func TestAgentHeartbeatUsesConnectionIPWhenAgentIPMissing(t *testing.T) {
	st, _ := store.Open("")
	reg, err := agentregistry.Open(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Upsert(agentregistry.Record{AgentID: "local-codex", AgentType: "codex"}); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil)
	s.SetAgentRegistry(reg)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/heartbeat", bytes.NewBufferString(`{"agent_id":"local-codex","agent_type":"codex","machine_name":"wsl-host"}`))
	req.RemoteAddr = "192.0.2.10:4567"
	rec := httptest.NewRecorder()
	s.apiAgentHeartbeat(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	got, ok := reg.Get("local-codex")
	if !ok {
		t.Fatal("agent was not retained")
	}
	if got.IP != "192.0.2.10" {
		t.Fatalf("IP=%q, want connection IP fallback", got.IP)
	}
	if got.MachineName != "wsl-host" {
		t.Fatalf("machine name=%q", got.MachineName)
	}
	if len(got.ObservedIPs) != 1 || got.ObservedIPs[0] != "192.0.2.10" {
		t.Fatalf("observed IPs=%v", got.ObservedIPs)
	}
}
