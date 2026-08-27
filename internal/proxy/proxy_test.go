package proxy

import (
	"path/filepath"
	"testing"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
)

func TestApplyRedactionsRewritesBytes(t *testing.T) {
	in := []byte(`token=ops_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345 # comment`)
	rs := []api.Redaction{{Match: "ops_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", Replace: "***"}}
	out := applyRedactions(in, rs)
	if string(out) != "token=*** # comment" {
		t.Fatalf("secret must be scrubbed, got %q", out)
	}
}

func TestApplyRedactionsLongestFirst(t *testing.T) {
	in := []byte("visit https://evil.example.com/x now")
	rs := []api.Redaction{
		{Match: "evil.example.com", Replace: "[host]"},
		{Match: "https://evil.example.com/x", Replace: "[url]"},
	}
	out := applyRedactions(in, rs)
	if string(out) != "visit [url] now" {
		t.Fatalf("longest match must win, got %q", out)
	}
}

func TestApplyRedactionsEmptyMatchNoop(t *testing.T) {
	in := []byte("payload")
	out := applyRedactions(in, []api.Redaction{{Path: "*", Replace: "***"}})
	if string(out) != "payload" {
		t.Fatalf("empty Match must not wipe payload, got %q", out)
	}
}

func TestCollectRedactionsMergesSignals(t *testing.T) {
	signals := []api.Signal{
		{Redactions: []api.Redaction{{Match: "a", Replace: "*"}}},
		{Redactions: []api.Redaction{{Match: "b", Replace: "*"}, {Match: "c", Replace: "*"}}},
		{},
	}
	got := collectRedactions(signals)
	if len(got) != 3 {
		t.Fatalf("want 3 redactions merged, got %d", len(got))
	}
}

func TestIsolationDecisionBlocksAndRestricts(t *testing.T) {
	r, err := agentregistry.Open(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(agentregistry.Record{AgentID: "agent-1", SessionID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{Agents: r}
	call := &api.ToolCall{Principal: api.Principal{AgentID: "agent-1"}, ToolID: "x", Action: "write"}
	for _, level := range []string{"paused", "isolated", "restricted"} {
		if _, err := r.SetIsolation("agent-1", level, "test"); err != nil {
			t.Fatal(err)
		}
		if d := g.isolationDecision(call); d == nil || d.Final != api.VerdictBlock {
			t.Fatalf("level %s must block write", level)
		}
	}
	if _, err := r.SetIsolation("agent-1", "restricted", "test"); err != nil {
		t.Fatal(err)
	}
	read := *call
	read.Action = "read"
	if d := g.isolationDecision(&read); d != nil {
		t.Fatal("restricted read should pass through")
	}
}
