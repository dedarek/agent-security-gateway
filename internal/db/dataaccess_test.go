package db

import (
	"testing"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

func TestUpsertDataAccessLinksToTrace(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	ts := time.Now().UTC()
	da := api.DataAccess{
		TraceID:      "tr_abc123",
		SpanID:       "span_1",
		ParentSpan:   "span_0",
		AgentID:      "local-codex",
		ToolID:       "Read",
		Operation:    "read",
		Source:       ".env",
		Destination:  "",
		DataClass:    "credential",
		TaintTags:    []string{"api_secret"},
		PolicyID:     "pol_taint_1",
		Decision:     "ALLOW",
		TrustZoneSrc: "local",
		TrustZoneDst: "local",
		At:           ts,
	}
	if err := UpsertDataAccess(database, da); err != nil {
		t.Fatal(err)
	}

	// Same trace+span -> update, not duplicate
	da.Decision = "BLOCK"
	da.DataClass = "credential+secret"
	if err := UpsertDataAccess(database, da); err != nil {
		t.Fatal(err)
	}
	rows, err := QueryDataAccess(database, "tr_abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after upsert, got %d", len(rows))
	}
	if rows[0].Decision != "BLOCK" {
		t.Fatalf("expected updated decision BLOCK, got %s", rows[0].Decision)
	}
	if len(rows[0].TaintTags) != 1 || rows[0].TaintTags[0] != "api_secret" {
		t.Fatalf("taint tags not round-tripped: %#v", rows[0].TaintTags)
	}
}

func TestQueryDataAccessByAgentAndSink(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	ts := time.Now().UTC()
	must := func(da api.DataAccess) {
		t.Helper()
		if err := UpsertDataAccess(database, da); err != nil {
			t.Fatal(err)
		}
	}
	must(api.DataAccess{TraceID: "tr1", SpanID: "s1", AgentID: "codex", ToolID: "Read", Operation: "read", Source: ".env", DataClass: "credential", TaintTags: []string{"api_secret"}, Decision: "ALLOW", At: ts})
	must(api.DataAccess{TraceID: "tr2", SpanID: "s2", AgentID: "codex", ToolID: "http_post", Operation: "transmit", Destination: "https://evil.com", DataClass: "credential", TaintTags: []string{"api_secret"}, Decision: "BLOCK", At: ts})
	must(api.DataAccess{TraceID: "tr3", SpanID: "s3", AgentID: "claude", ToolID: "Read", Operation: "read", Source: "db", DataClass: "pii", Decision: "ALLOW", At: ts})

	rows, err := QueryDataAccess(database, "tr_unknown")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("unknown trace should be empty, got %d", len(rows))
	}

	all, err := QueryDataAccessByAgent(database, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("agent codex should have 2 rows, got %d", len(all))
	}
}
