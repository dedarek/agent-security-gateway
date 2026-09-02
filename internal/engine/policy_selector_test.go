package engine

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/dedarek/agent-security-gateway/api"
)

func policyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:policy-selector-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE policies (
		id INTEGER PRIMARY KEY, agent_id TEXT, axis TEXT, rule_id TEXT NOT NULL,
		action TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
		selector_json TEXT NOT NULL DEFAULT '{}', updated_at INTEGER NOT NULL
	)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func TestStructuredSelectorPrefersSensitiveReadRule(t *testing.T) {
	db := policyTestDB(t)
	defer db.Close()
	_, err := db.Exec(`INSERT INTO policies(agent_id,axis,rule_id,action,enabled,selector_json,updated_at)
		VALUES(?,?,?,?,?,?,?)`, "agent-1", "permission", "Read", "allow", 1,
		`{"kind":"capability","capability":"filesystem","tool":"Read","operation":"read"}`, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO policies(agent_id,axis,rule_id,action,enabled,selector_json,updated_at)
		VALUES(?,?,?,?,?,?,?)`, "agent-1", "permission", "Read:sensitive", "block", 1,
		`{"kind":"capability","capability":"filesystem","tool":"Read","path_class":"sensitive"}`, 1)
	if err != nil {
		t.Fatal(err)
	}

	e := NewPolicyEngine(db)
	sig, err := e.EvaluatePre(context.Background(), &api.ToolCall{
		ToolID:    "Read",
		Action:    "read",
		Arguments: []byte(`{"path":"/home/a/.ssh/id_ed25519"}`),
		Principal: api.Principal{AgentID: "agent-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sig == nil || sig.Verdict != api.VerdictBlock {
		t.Fatalf("expected sensitive read to block, got %+v", sig)
	}
}

func TestStructuredMCPToolSelectorMatchesServerAndTool(t *testing.T) {
	db := policyTestDB(t)
	defer db.Close()
	_, err := db.Exec(`INSERT INTO policies(agent_id,axis,rule_id,action,enabled,selector_json,updated_at)
		VALUES(?,?,?,?,?,?,?)`, "agent-2", "permission", "MCP:tool", "confirm", 1,
		`{"kind":"mcp","feature":"tool","server":"github","tool":"create_issue","operation":"call"}`, 1)
	if err != nil {
		t.Fatal(err)
	}
	e := NewPolicyEngine(db)
	sig, err := e.EvaluatePre(context.Background(), &api.ToolCall{
		ToolID:    "github.create_issue",
		Resource:  "github",
		Action:    "write",
		Principal: api.Principal{AgentID: "agent-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sig == nil || sig.Verdict != api.VerdictConfirm {
		t.Fatalf("expected MCP tool confirmation, got %+v", sig)
	}
}
