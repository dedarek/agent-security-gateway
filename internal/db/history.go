package db

import (
	"database/sql"
	"time"
)

// InsertModelHistory records a model switch.
func InsertModelHistory(db *sql.DB, agentID, fromModel, toModel, source string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := db.Exec(`
INSERT INTO model_history(agent_id, ts, from_model, to_model, source)
VALUES(?,?,?,?,?)`, agentID, at.UnixMilli(), fromModel, toModel, source)
	return err
}

// ModelHistoryEntry is one row.
type ModelHistoryEntry struct {
	AgentID   string    `json:"agent_id"`
	At        time.Time `json:"at"`
	FromModel string    `json:"from_model"`
	ToModel   string    `json:"to_model"`
	Source    string    `json:"source"`
}

// ListModelHistory returns history for an agent, newest first.
func ListModelHistory(db *sql.DB, agentID string, limit int) ([]ModelHistoryEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.Query(`
SELECT ts, from_model, to_model, source FROM model_history
WHERE agent_id=? ORDER BY ts DESC, id DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelHistoryEntry
	for rows.Next() {
		var ts int64
		var e ModelHistoryEntry
		e.AgentID = agentID
		if err := rows.Scan(&ts, &e.FromModel, &e.ToModel, &e.Source); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(ts).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
