package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

// data_access records a data-flow hop in the agent trace graph. One row per
// (trace_id, span_id): the same tool touching the same data in the same span
// updates rather than duplicates.
const dataAccessSchema = `
CREATE TABLE IF NOT EXISTS data_access (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_id      TEXT NOT NULL,
  span_id       TEXT NOT NULL,
  parent_span   TEXT NOT NULL DEFAULT '',
  agent_id      TEXT NOT NULL DEFAULT '',
  tool_id       TEXT NOT NULL DEFAULT '',
  operation     TEXT NOT NULL,
  source        TEXT NOT NULL DEFAULT '',
  destination   TEXT NOT NULL DEFAULT '',
  data_class    TEXT NOT NULL DEFAULT '',
  taint_tags    TEXT NOT NULL DEFAULT '[]',
  policy_id     TEXT NOT NULL DEFAULT '',
  decision      TEXT NOT NULL DEFAULT '',
  trust_zone_src TEXT NOT NULL DEFAULT '',
  trust_zone_dst TEXT NOT NULL DEFAULT '',
  ts            INTEGER NOT NULL,
  UNIQUE(trace_id, span_id)
);
CREATE INDEX IF NOT EXISTS idx_data_access_trace ON data_access(trace_id, ts);
CREATE INDEX IF NOT EXISTS idx_data_access_agent ON data_access(agent_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_data_access_sink ON data_access(destination, ts DESC);
`

// UpsertDataAccess records one data-flow hop. Same (trace_id, span_id) updates
// the row so a span's final decision is authoritative.
func UpsertDataAccess(db *sql.DB, da api.DataAccess) error {
	if db == nil {
		return fmt.Errorf("nil database")
	}
	tags, err := json.Marshal(nonNilStrings(da.TaintTags))
	if err != nil {
		return fmt.Errorf("marshal taint tags: %w", err)
	}
	ts := da.At.UnixMilli()
	if da.At.IsZero() {
		ts = time.Now().UnixMilli()
	}
	_, err = db.Exec(`
INSERT INTO data_access(trace_id, span_id, parent_span, agent_id, tool_id, operation, source, destination, data_class, taint_tags, policy_id, decision, trust_zone_src, trust_zone_dst, ts)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(trace_id, span_id) DO UPDATE SET
  parent_span=excluded.parent_span, agent_id=excluded.agent_id, tool_id=excluded.tool_id,
  operation=excluded.operation, source=excluded.source, destination=excluded.destination,
  data_class=excluded.data_class, taint_tags=excluded.taint_tags, policy_id=excluded.policy_id,
  decision=excluded.decision, trust_zone_src=excluded.trust_zone_src, trust_zone_dst=excluded.trust_zone_dst,
  ts=excluded.ts
`, da.TraceID, da.SpanID, da.ParentSpan, da.AgentID, da.ToolID, da.Operation, da.Source, da.Destination, da.DataClass, string(tags), da.PolicyID, da.Decision, da.TrustZoneSrc, da.TrustZoneDst, ts)
	return err
}

// QueryDataAccess returns all data-flow hops for a trace (lineage graph edges).
func QueryDataAccess(db *sql.DB, traceID string) ([]api.DataAccess, error) {
	rows, err := db.Query(`SELECT trace_id, span_id, parent_span, agent_id, tool_id, operation, source, destination, data_class, taint_tags, policy_id, decision, trust_zone_src, trust_zone_dst, ts FROM data_access WHERE trace_id=? ORDER BY ts ASC, id ASC`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDataAccessRows(rows)
}

// QueryDataAccessByAgent returns data-flow hops for an agent, newest first.
func QueryDataAccessByAgent(db *sql.DB, agentID string) ([]api.DataAccess, error) {
	rows, err := db.Query(`SELECT trace_id, span_id, parent_span, agent_id, tool_id, operation, source, destination, data_class, taint_tags, policy_id, decision, trust_zone_src, trust_zone_dst, ts FROM data_access WHERE agent_id=? ORDER BY ts DESC, id DESC LIMIT 500`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDataAccessRows(rows)
}

func scanDataAccessRows(rows *sql.Rows) ([]api.DataAccess, error) {
	var out []api.DataAccess
	for rows.Next() {
		var da api.DataAccess
		var tags string
		var ts int64
		if err := rows.Scan(&da.TraceID, &da.SpanID, &da.ParentSpan, &da.AgentID, &da.ToolID, &da.Operation, &da.Source, &da.Destination, &da.DataClass, &tags, &da.PolicyID, &da.Decision, &da.TrustZoneSrc, &da.TrustZoneDst, &ts); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &da.TaintTags)
		da.At = time.UnixMilli(ts).UTC()
		out = append(out, da)
	}
	return out, rows.Err()
}
