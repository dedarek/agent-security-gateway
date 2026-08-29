package kg

import (
	"testing"

	"github.com/dedarek/agent-security-gateway/api"
)

// Hook path carries the real agent in Principal.AgentID (UserID empty);
// the graph must link the event to that concrete agent so lineage works.
func TestIngestUsesAgentID(t *testing.T) {
	b := NewBuilder()
	b.Ingest(api.Event{
		SessionID: "sess-1",
		Call: api.ToolCall{
			CallID:    "hook-1",
			ToolID:    "Bash",
			Principal: api.Principal{AgentID: "sectest-console"},
		},
		Decision: api.Decision{Final: api.VerdictBlock, Risk: 93},
	})
	ents, rels := b.Export()
	var agentFound bool
	for _, e := range ents {
		if e["id"] == "agent:sectest-console" {
			agentFound = true
		}
		if e["type"] == "Trace" {
			t.Fatalf("Trace node must not be emitted: %v", e["id"])
		}
	}
	if !agentFound {
		ids := []string{}
		for _, e := range ents {
			ids = append(ids, e["id"].(string))
		}
		t.Fatalf("agent:sectest-console missing; got %v", ids)
	}
	var performed bool
	for _, r := range rels {
		if r["source"] == "agent:sectest-console" && r["type"] == "performed" && r["target"] == "evt:hook-1" {
			performed = true
		}
	}
	if !performed {
		t.Fatalf("expected performed edge agent:sectest-console -> evt:hook-1; got %v", rels)
	}
}

// Proxy path (UserID set, AgentID empty) still resolves to a stable agent id.
func TestIngestFallsBackToUserID(t *testing.T) {
	b := NewBuilder()
	b.Ingest(api.Event{
		SessionID: "s",
		Call:      api.ToolCall{CallID: "c1", ToolID: "Read", Principal: api.Principal{UserID: "svc-a"}},
	})
	ents, _ := b.Export()
	for _, e := range ents {
		if e["id"] == "agent:svc-a" {
			return
		}
	}
	t.Fatal("agent:svc-a missing")
}
