package db

import (
	"database/sql"
	"encoding/json"
	"os"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

// InsertEvent appends an event row. payload is the full api.Event JSON.
func InsertEvent(db *sql.DB, ev api.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ts := ev.Timestamp.UnixMilli()
	if ev.Timestamp.IsZero() {
		ts = time.Now().UnixMilli()
	}
	agentID := ev.Call.Principal.AgentID
	if agentID == "" {
		agentID = ev.SessionID
	}
	toolName := ev.Call.ToolID
	kind := "tool"
	if ev.Call.ToolID != "" && len(ev.Call.ToolID) > 4 && ev.Call.ToolID[:4] == "llm." {
		kind = "llm"
	}
	_, err = db.Exec(`
INSERT INTO events(ts, agent_id, session_id, trace_id, parent_id, kind, tool_name, verdict, risk, payload)
VALUES(?,?,?,?,?,?,?,?,?,?)
`, ts, agentID, ev.SessionID, ev.TraceID, ev.ParentID, kind, toolName, ev.Decision.Final.String(), ev.Decision.Risk, string(b))
	return err
}

// QueryEvents returns events ordered by ts DESC with pagination.
func QueryEvents(db *sql.DB, agentID, sessionID, verdict string, offset, limit int) ([]api.Event, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	where := "WHERE 1=1"
	var args []any
	if agentID != "" {
		where += " AND agent_id=?"
		args = append(args, agentID)
	}
	if sessionID != "" {
		where += " AND session_id=?"
		args = append(args, sessionID)
	}
	if verdict != "" {
		where += " AND verdict=?"
		args = append(args, verdict)
	}
	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `SELECT payload FROM events ` + where + ` ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []api.Event
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, 0, err
		}
		var ev api.Event
		if err := json.Unmarshal([]byte(j), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, total, rows.Err()
}

// RecentEvents returns n newest events.
func RecentEvents(db *sql.DB, n int) ([]api.Event, error) {
	rows, err := db.Query(`SELECT payload FROM events ORDER BY ts DESC, id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []api.Event
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		var ev api.Event
		if err := json.Unmarshal([]byte(j), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Trajectory returns all events for a session in chronological order.
func Trajectory(db *sql.DB, sessionID string) ([]api.Event, error) {
	rows, err := db.Query(`SELECT payload FROM events WHERE session_id=? ORDER BY ts ASC, id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []api.Event
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		var ev api.Event
		if err := json.Unmarshal([]byte(j), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CountEvents returns total event count.
func CountEvents(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
	return n, err
}

// TrimEvents deletes oldest events keeping at most maxEvents newest.
func TrimEvents(db *sql.DB, maxEvents int) (int64, error) {
	if maxEvents <= 0 {
		return 0, nil
	}
	res, err := db.Exec(`
DELETE FROM events WHERE id IN (
  SELECT id FROM events ORDER BY id DESC LIMIT -1 OFFSET ?
)`, maxEvents)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MigrateEventsFromJSONL imports events from a JSONL file.
func MigrateEventsFromJSONL(db *sql.DB, path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	lines := splitLines(data)
	n := 0
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var ev api.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Call.CallID == "" {
			continue
		}
		if err := InsertEvent(db, ev); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range data {
		if c == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}
