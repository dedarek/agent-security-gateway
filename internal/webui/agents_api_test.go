package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

func TestAgentsAPIShowsOnlyCurrentRuntimeAgents(t *testing.T) {
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agents.json")
	now := time.Now().UTC()
	fixture := map[string]agentregistry.Record{
		"current-opencode": {AgentID: "current-opencode", SessionID: "s-1", AgentType: "opencode", ProbeID: "probe-current", MachineID: "m-current", LastHeartbeat: now, LastActivity: now},
		"old-test":         {AgentID: "old-test", SessionID: "s-old", AgentType: "opencode", ProbeID: "probe-old", MachineID: "m-old", LastHeartbeat: now.Add(-6 * time.Minute), LastActivity: now.Add(-6 * time.Minute)},
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := agentregistry.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	s := New(st, nil, nil)
	s.SetAgentRegistry(reg)
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	res := httptest.NewRecorder()
	s.apiAgents(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	var got []AgentInfo
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("API agents = %#v", got)
	}
	// Should return both, with offline status for stale agent.
	m := map[string]string{}
	for _, a := range got {
		m[a.AgentID] = a.Status
	}
	if m["current-opencode"] != "online" || m["old-test"] != "offline" {
		t.Fatalf("status map = %#v", m)
	}
}
