package agentregistry

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
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
		// Harness-level online requires recent activity, not just heartbeat.
		_ = r.ObserveSession(id, id+"-session", time.Now().UTC())
	}
	r.records["zeta"] = func() Record {
		v := r.records["zeta"]
		v.LastActivity = time.Now().Add(-6 * time.Minute)
		v.LastHeartbeat = time.Now().Add(-6 * time.Minute)
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
	// Heartbeat alone doesn't drive online; need real activity first.
	if err := r.ObserveSession("a-1", "s-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := r.Heartbeat("a-1", "10.0.0.2", []string{"10.0.0.2"}, "model-2", "provider-2", "opencode", "Agent A", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Get("a-1")
	if got.Status != "active" || got.IP != "10.0.0.2" {
		t.Fatalf("expected heartbeat IP, got %q status %q", got.IP, got.Status)
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

func TestComputeStatus(t *testing.T) {
	now := time.Now().UTC()

	// --- fallback without DB: lastActivity driven ---
	t.Run("active via lastActivity", func(t *testing.T) {
		r, _ := Open(filepath.Join(t.TempDir(), "agents.json"))
		rec := Record{AgentID: "a-active", LastActivity: now.Add(-2 * time.Minute), LastHeartbeat: now.Add(-10 * time.Minute)}
		if got := r.computeStatus(rec, now); got != "active" {
			t.Fatalf("expected active, got %q", got)
		}
		if got := computeStatus(rec, now); got != "active" {
			t.Fatalf("free computeStatus expected active, got %q", got)
		}
	})
	t.Run("idle via heartbeatWindow", func(t *testing.T) {
		r, _ := Open(filepath.Join(t.TempDir(), "agents.json"))
		rec := Record{AgentID: "a-idle", LastActivity: now.Add(-10 * time.Minute), LastHeartbeat: now.Add(-1 * time.Minute)}
		if got := r.computeStatus(rec, now); got != "idle" {
			t.Fatalf("expected idle, got %q", got)
		}
		if !isActiveAt(rec.LastHeartbeat, now) {
			t.Fatalf("isActiveAt should be true within heartbeatWindow")
		}
		if isActiveAt(now.Add(-5*time.Minute), now) {
			t.Fatalf("isActiveAt should be false outside heartbeatWindow")
		}
		// isActive uses heartbeatWindow
		if !isActive(now.Add(-1 * time.Minute)) {
			t.Fatalf("isActive should be true within heartbeatWindow")
		}
	})
	t.Run("offline when stale", func(t *testing.T) {
		r, _ := Open(filepath.Join(t.TempDir(), "agents.json"))
		rec := Record{AgentID: "a-off", LastActivity: now.Add(-10 * time.Minute), LastHeartbeat: now.Add(-5 * time.Minute)}
		if got := r.computeStatus(rec, now); got != "offline" {
			t.Fatalf("expected offline, got %q", got)
		}
	})

	// --- DB-driven: recent events within 5m => active ---
	t.Run("active driven by recent events", func(t *testing.T) {
		sqlDB := openTestDB(t)
		defer sqlDB.Close()
		r, err := OpenWithDB(sqlDB, "")
		if err != nil {
			t.Fatal(err)
		}
		agentID := "a-evt"
		// stale activity/heartbeat would otherwise be offline
		r.records[agentID] = Record{AgentID: agentID, LastActivity: now.Add(-10 * time.Minute), LastHeartbeat: now.Add(-10 * time.Minute)}
		// no event yet -> offline
		if got := r.computeStatus(r.records[agentID], now); got != "offline" {
			t.Fatalf("expected offline before event, got %q", got)
		}
		// insert recent event (ts within 5m)
		ts := now.Add(-2 * time.Minute).UnixMilli()
		if _, err := sqlDB.Exec(`INSERT INTO events(ts, agent_id, session_id, trace_id, parent_id, kind, tool_name, verdict, risk, payload) VALUES(?,?,?,?,?,?,?,?,?,?)`, ts, agentID, "s1", "", "", "tool", "test", "ALLOW", 0, "{}"); err != nil {
			t.Fatal(err)
		}
		if got := r.computeStatus(r.records[agentID], now); got != "active" {
			t.Fatalf("expected active via recent event, got %q", got)
		}
		// List/Get should also reflect active via DB
		r.records[agentID] = Record{AgentID: agentID, LastActivity: now.Add(-10 * time.Minute), LastHeartbeat: now.Add(-10 * time.Minute)}
		// simulate persistence: Get recomputes
		// need to keep record in map
		gotRec, ok := r.Get(agentID)
		if !ok || gotRec.Status != "active" {
			t.Fatalf("Get should be active via events, got ok=%v status=%q", ok, gotRec.Status)
		}
		list := r.List()
		found := false
		for _, v := range list {
			if v.AgentID == agentID && v.Status == "active" {
				found = true
			}
		}
		if !found {
			t.Fatalf("List should contain active via events, got %#v", list)
		}
	})
	t.Run("stale event does not drive active", func(t *testing.T) {
		sqlDB := openTestDB(t)
		defer sqlDB.Close()
		r, err := OpenWithDB(sqlDB, "")
		if err != nil {
			t.Fatal(err)
		}
		agentID := "a-stale-evt"
		r.records[agentID] = Record{AgentID: agentID, LastActivity: now.Add(-10 * time.Minute), LastHeartbeat: now.Add(-10 * time.Minute)}
		ts := now.Add(-6 * time.Minute).UnixMilli()
		if _, err := sqlDB.Exec(`INSERT INTO events(ts, agent_id, session_id, trace_id, parent_id, kind, tool_name, verdict, risk, payload) VALUES(?,?,?,?,?,?,?,?,?,?)`, ts, agentID, "s1", "", "", "tool", "test", "ALLOW", 0, "{}"); err != nil {
			t.Fatal(err)
		}
		if got := r.computeStatus(r.records[agentID], now); got != "offline" {
			t.Fatalf("expected offline for stale event (>5m), got %q", got)
		}
		// heartbeat fallback still works
		r.records[agentID] = Record{AgentID: agentID, LastActivity: now.Add(-10 * time.Minute), LastHeartbeat: now.Add(-1 * time.Minute)}
		if got := r.computeStatus(r.records[agentID], now); got != "idle" {
			t.Fatalf("expected idle via heartbeat when event stale, got %q", got)
		}
	})
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS agents (
  agent_id TEXT PRIMARY KEY,
  record_json TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'offline',
  last_activity INTEGER NOT NULL DEFAULT 0,
  last_heartbeat INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  agent_id TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  trace_id TEXT NOT NULL DEFAULT '',
  parent_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT 'tool',
  tool_name TEXT NOT NULL DEFAULT '',
  verdict TEXT NOT NULL DEFAULT 'ALLOW',
  risk INTEGER NOT NULL DEFAULT 0,
  payload TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_agent_ts ON events(agent_id, ts DESC);
`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}
