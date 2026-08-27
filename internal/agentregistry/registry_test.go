package agentregistry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpsertRejectsEmptyAgentID(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(Record{SessionID: "orphan-session"}); err == nil {
		t.Fatal("expected empty AgentID to be rejected")
	}
}

func TestOpenSkipsInvalidAgentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.json")
	b, err := json.Marshal(map[string]Record{
		"agents": {SessionID: "", AgentID: ""},
		"good":   {AgentID: "good", SessionID: "good-session"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get(""); ok {
		t.Fatal("invalid empty identity was loaded")
	}
	if _, ok := r.Get("agents"); ok {
		t.Fatal("wrapper key was promoted to an Agent identity")
	}
	if _, ok := r.Get("good"); !ok {
		t.Fatal("valid identity was dropped")
	}
}

func TestListActiveExcludesStaleAgentsAndSorts(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"zeta", "alpha"} {
		if err := r.Upsert(Record{AgentID: id, SessionID: id + "-session"}); err != nil {
			t.Fatal(err)
		}
	}
	r.records["zeta"] = func() Record {
		v := r.records["zeta"]
		v.LastHeartbeat = time.Now().Add(-2 * time.Minute)
		return v
	}()
	active := r.ListActive()
	if len(active) != 1 || active[0].AgentID != "alpha" {
		t.Fatalf("active agents = %#v", active)
	}
	if strings.TrimSpace(active[0].AgentID) == "" {
		t.Fatal("active list returned an empty identity")
	}
}

func TestUpsertHeartbeatAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	base := Record{AgentID: "a-1", MachineName: "host", ProcessID: 10}
	if err := r.Upsert(base); err != nil {
		t.Fatal(err)
	}
	base.ProcessID = 11
	if err := r.Upsert(base); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("a-1")
	if !ok || got.RestartCount != 1 {
		t.Fatalf("expected one record and one restart, got ok=%v count=%d", ok, got.RestartCount)
	}
	if err := r.Heartbeat("a-1", "10.0.0.2", []string{"10.0.0.2"}, "model-2", "provider-2", "opencode", "Agent A", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Get("a-1")
	if got.Status != "online" || got.IP != "10.0.0.2" {
		t.Fatalf("expected heartbeat IP, got %q", got.IP)
	}
	if err := r.ObserveModel("a-1", "actual-model", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := r.Heartbeat("a-1", "10.0.0.3", []string{"10.0.0.3"}, "declared-model", "provider-3", "opencode", "Agent A", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Get("a-1")
	if got.Model != "actual-model" || got.ObservedModel != "actual-model" || got.DeclaredModel != "declared-model" {
		t.Fatalf("observed model must win over heartbeat declaration: %+v", got)
	}
	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.Get("a-1"); !ok {
		t.Fatal("record was not persisted")
	}
}

func TestIsolationChangesPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(Record{AgentID: "a-2", SessionID: "s-2"}); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"paused", "restricted", "isolated", "active"} {
		if _, err := r.SetIsolation("a-2", level, "test"); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := r.Get("a-2")
	if !ok || got.Isolation != "active" || len(got.Changes) != 4 {
		t.Fatalf("isolation history not retained: ok=%v isolation=%q changes=%d", ok, got.Isolation, len(got.Changes))
	}
	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok = r2.Get("a-2")
	if !ok || got.Isolation != "active" || len(got.Changes) != 4 {
		t.Fatalf("isolation history not persisted: ok=%v isolation=%q changes=%d", ok, got.Isolation, len(got.Changes))
	}
}
