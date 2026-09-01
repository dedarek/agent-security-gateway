// Package db provides the SQLite persistence layer for ASG.
//
// M2 introduces SQLite (modernc.org/sqlite, pure Go, no CGO) as the
// primary store for agents, events, activity steps and model history.
// JSONL files are retained as append-only audit原件 when configured.
//
// Schema is applied via Migrate; all tables are created IF NOT EXISTS so
// the DB file can be created on first run with zero migration tooling.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS agents (
  agent_id          TEXT PRIMARY KEY,
  record_json       TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'offline',
  last_activity     INTEGER NOT NULL DEFAULT 0,
  last_heartbeat    INTEGER NOT NULL DEFAULT 0,
  updated_at        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_agents_activity ON agents(last_activity DESC);
CREATE INDEX IF NOT EXISTS idx_agents_heartbeat ON agents(last_heartbeat DESC);

CREATE TABLE IF NOT EXISTS events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          INTEGER NOT NULL,
  agent_id    TEXT NOT NULL DEFAULT '',
  session_id  TEXT NOT NULL DEFAULT '',
  trace_id    TEXT NOT NULL DEFAULT '',
  parent_id   TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT 'tool',
  tool_name   TEXT NOT NULL DEFAULT '',
  verdict     TEXT NOT NULL DEFAULT 'ALLOW',
  risk        INTEGER NOT NULL DEFAULT 0,
  payload     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_agent_ts ON events(agent_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id, ts);
CREATE INDEX IF NOT EXISTS idx_events_trace ON events(trace_id, ts);
CREATE INDEX IF NOT EXISTS idx_events_verdict ON events(verdict);
CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts DESC);

CREATE TABLE IF NOT EXISTS activity_steps (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          INTEGER NOT NULL,
  agent_id    TEXT NOT NULL,
  session_id  TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT 'tool_use',
  tool_name   TEXT NOT NULL DEFAULT '',
  summary     TEXT NOT NULL DEFAULT '',
  verdict     TEXT NOT NULL DEFAULT '',
  payload     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_activity_agent_ts ON activity_steps(agent_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_activity_session ON activity_steps(session_id, ts);

CREATE TABLE IF NOT EXISTS model_history (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_id    TEXT NOT NULL,
  ts          INTEGER NOT NULL,
  from_model  TEXT NOT NULL DEFAULT '',
  to_model    TEXT NOT NULL,
  source      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_model_history_agent ON model_history(agent_id, ts DESC);

CREATE TABLE IF NOT EXISTS inventory_items (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  stable_key         TEXT NOT NULL UNIQUE,
  agent_id           TEXT NOT NULL DEFAULT '',
  parent_id          TEXT NOT NULL DEFAULT '',
  kind               TEXT NOT NULL,
  name               TEXT NOT NULL,
  source             TEXT NOT NULL DEFAULT '',
  origin             TEXT NOT NULL DEFAULT '',
  version            TEXT NOT NULL DEFAULT '',
  manifest_hash      TEXT NOT NULL DEFAULT '',
  schema_hash        TEXT NOT NULL DEFAULT '',
  install_path       TEXT NOT NULL DEFAULT '',
  status             TEXT NOT NULL DEFAULT 'discovered',
  risk_level         TEXT NOT NULL DEFAULT '',
  risk_labels_json   TEXT NOT NULL DEFAULT '[]',
  risk_reasons_json  TEXT NOT NULL DEFAULT '[]',
  declared_caps_json TEXT NOT NULL DEFAULT '[]',
  observed_caps_json TEXT NOT NULL DEFAULT '[]',
  ai_status          TEXT NOT NULL DEFAULT 'pending',
  policy_json        TEXT NOT NULL DEFAULT '{}',
  first_seen         INTEGER NOT NULL,
  last_seen          INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_inventory_agent_seen ON inventory_items(agent_id, last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_inventory_kind ON inventory_items(kind);
CREATE INDEX IF NOT EXISTS idx_inventory_status ON inventory_items(status);

CREATE TABLE IF NOT EXISTS policies (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_id    TEXT,
  axis        TEXT NOT NULL,
  rule_id     TEXT NOT NULL,
  action      TEXT NOT NULL,
  enabled     INTEGER NOT NULL DEFAULT 1,
  selector_json TEXT NOT NULL DEFAULT '{}',
  updated_at  INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_policies_scope ON policies(COALESCE(agent_id,''), rule_id);
`

// Open opens (creating parent dirs if needed) a SQLite DB at dsn and
// applies the schema. dsn is a file path; use ":memory:" for tests.
// For file DSNs, WAL mode is enabled for concurrent readers.
func Open(dsn string) (*sql.DB, error) {
	if dsn == "" || dsn == ":memory:" {
		dsn = ":memory:"
	} else {
		if dir := filepath.Dir(dsn); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create db dir: %w", err)
			}
		}
		// modernc sqlite DSN: file path with query params
		// Use _pragma to set busy timeout so concurrent writers don't fail immediately.
		if dsn != ":memory:" && !hasQuery(dsn) {
			dsn = dsn + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single writer at a time is fine for ASG's workload; limit to 1 writer
	// connection to avoid SQLITE_BUSY on concurrent writes.
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// M2 databases predate structured policy selectors. Keep the old
	// agent_id+rule_id API and add the selector column in place.
	if _, err := db.Exec(`ALTER TABLE policies ADD COLUMN selector_json TEXT NOT NULL DEFAULT '{}'`); err != nil && !isDuplicateColumn(err) {
		_ = db.Close()
		return nil, fmt.Errorf("migrate policy selectors: %w", err)
	}
	return db, nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "duplicate column") || strings.Contains(strings.ToLower(err.Error()), "already exists"))
}

func hasQuery(dsn string) bool {
	for _, c := range dsn {
		if c == '?' {
			return true
		}
	}
	return false
}

// Ping verifies the DB is reachable.
func Ping(db *sql.DB) error {
	return db.Ping()
}
