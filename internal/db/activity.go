package db

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/activity"
)

// InsertActivityStep appends an activity step.
func InsertActivityStep(db *sql.DB, s activity.Step) error {
	ts := s.At.UnixMilli()
	if s.At.IsZero() {
		ts = time.Now().UnixMilli()
	}
	var payload string
	if len(s.Raw) > 0 {
		payload = string(s.Raw)
	}
	_, err := db.Exec(`
INSERT INTO activity_steps(ts, agent_id, session_id, kind, tool_name, summary, verdict, payload)
VALUES(?,?,?,?,?,?,?,?)
`, ts, s.AgentID, s.SessionID, s.Kind, s.ToolName, s.Summary, s.Verdict, payload)
	return err
}

// ListActivitySteps returns steps for an agent, oldest first.
func ListActivitySteps(db *sql.DB, agentID string, limit int) ([]activity.Step, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := db.Query(`
SELECT ts, agent_id, session_id, kind, tool_name, summary, verdict, payload
FROM activity_steps WHERE agent_id=? ORDER BY ts ASC, id ASC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activity.Step
	for rows.Next() {
		var ts int64
		var s activity.Step
		var payload string
		if err := rows.Scan(&ts, &s.AgentID, &s.SessionID, &s.Kind, &s.ToolName, &s.Summary, &s.Verdict, &payload); err != nil {
			return nil, err
		}
		s.At = time.UnixMilli(ts).UTC()
		if payload != "" {
			s.Raw = json.RawMessage(payload)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountActivitySteps returns the count for an agent.
func CountActivitySteps(db *sql.DB, agentID string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM activity_steps WHERE agent_id=?`, agentID).Scan(&n)
	return n, err
}

// TrimActivitySteps keeps at most maxPerAgent newest steps per agent.
func TrimActivitySteps(db *sql.DB, agentID string, maxPerAgent int) (int64, error) {
	if maxPerAgent <= 0 {
		return 0, nil
	}
	res, err := db.Exec(`
DELETE FROM activity_steps WHERE agent_id=? AND id IN (
  SELECT id FROM activity_steps WHERE agent_id=? ORDER BY id DESC LIMIT -1 OFFSET ?
)`, agentID, agentID, maxPerAgent)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
