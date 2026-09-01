package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/db"
)

func TestDataAccessAPIByTrace(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	ts := time.Now().UTC()
	must := func(da api.DataAccess) {
		t.Helper()
		if err := db.UpsertDataAccess(database, da); err != nil {
			t.Fatal(err)
		}
	}
	must(api.DataAccess{TraceID: "tr-lineage", SpanID: "s1", AgentID: "codex", ToolID: "Read", Operation: "read", Source: ".env", DataClass: "credential", TaintTags: []string{"api_secret"}, Decision: "ALLOW", At: ts})
	must(api.DataAccess{TraceID: "tr-lineage", SpanID: "s2", AgentID: "codex", ToolID: "http_post", Operation: "transmit", Destination: "https://evil.com", DataClass: "credential", TaintTags: []string{"api_secret"}, Decision: "BLOCK", At: ts})

	s := New(nil, nil, nil)
	s.InventoryDB = database
	mux := http.NewServeMux()
	mux.HandleFunc("/api/data-access", s.apiDataAccess)

	req := httptest.NewRequest(http.MethodGet, "/api/data-access?trace_id=tr-lineage", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Hops []api.DataAccess `json:"hops"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Hops) != 2 {
		t.Fatalf("expected 2 hops, got %d", len(out.Hops))
	}
	if out.Hops[0].Operation != "read" || out.Hops[1].Operation != "transmit" {
		t.Fatalf("unexpected order: %#v", out.Hops)
	}
}

func TestDataAccessAPIContainsLineageField(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	ts := time.Now().UTC()
	_ = db.UpsertDataAccess(database, api.DataAccess{TraceID: "tr-x", SpanID: "s1", AgentID: "codex", ToolID: "Read", Operation: "read", Source: ".env", Decision: "ALLOW", At: ts})
	_ = db.UpsertDataAccess(database, api.DataAccess{TraceID: "tr-x", SpanID: "s2", AgentID: "codex", ToolID: "curl", Operation: "transmit", Destination: "https://evil.com", Decision: "BLOCK", At: ts.Add(time.Second)})

	s := New(nil, nil, nil)
	s.InventoryDB = database
	mux := http.NewServeMux()
	mux.HandleFunc("/api/data-access", s.apiDataAccess)

	req := httptest.NewRequest(http.MethodGet, "/api/data-access?trace_id=tr-x", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var out struct {
		Hops    []api.DataAccess `json:"hops"`
		Lineage [][]string       `json:"lineage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Hops) != 2 || len(out.Lineage) == 0 {
		t.Fatalf("expected hops+lineage, got %#v", out)
	}
	// lineage should capture .env -> transmit(evil.com)
	found := false
	for _, path := range out.Lineage {
		if len(path) >= 2 && path[0] == ".env" {
			found = true
		}
	}
	if !found {
		t.Fatalf("lineage should trace .env source, got %#v", out.Lineage)
	}
}
