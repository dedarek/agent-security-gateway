package db

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
)

// UpsertAgent inserts or replaces an agent row.
func UpsertAgent(db *sql.DB, rec agentregistry.Record) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	status := rec.Status
	if strings.TrimSpace(status) == "" {
		status = "offline"
	}
	now := time.Now().UnixMilli()
	la := int64(0)
	if !rec.LastActivity.IsZero() {
		la = rec.LastActivity.UnixMilli()
	}
	lh := int64(0)
	if !rec.LastHeartbeat.IsZero() {
		lh = rec.LastHeartbeat.UnixMilli()
	}
	_, err = db.Exec(`
INSERT INTO agents(agent_id, record_json, status, last_activity, last_heartbeat, updated_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(agent_id) DO UPDATE SET
  record_json=excluded.record_json,
  status=excluded.status,
  last_activity=excluded.last_activity,
  last_heartbeat=excluded.last_heartbeat,
  updated_at=excluded.updated_at
`, rec.AgentID, string(b), status, la, lh, now)
	return err
}

// GetAgent loads one agent by id.
func GetAgent(db *sql.DB, agentID string) (agentregistry.Record, bool, error) {
	var j string
	err := db.QueryRow(`SELECT record_json FROM agents WHERE agent_id=?`, agentID).Scan(&j)
	if err == sql.ErrNoRows {
		return agentregistry.Record{}, false, nil
	}
	if err != nil {
		return agentregistry.Record{}, false, err
	}
	var rec agentregistry.Record
	if err := json.Unmarshal([]byte(j), &rec); err != nil {
		return agentregistry.Record{}, false, err
	}
	return rec, true, nil
}

// ListAgents returns all agents ordered by agent_id.
func ListAgents(db *sql.DB) ([]agentregistry.Record, error) {
	rows, err := db.Query(`SELECT record_json FROM agents ORDER BY agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agentregistry.Record
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		var rec agentregistry.Record
		if err := json.Unmarshal([]byte(j), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteAgent removes an agent row.
func DeleteAgent(db *sql.DB, agentID string) error {
	_, err := db.Exec(`DELETE FROM agents WHERE agent_id=?`, agentID)
	return err
}

// CountAgents returns the number of agent rows.
func CountAgents(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM agents`).Scan(&n)
	return n, err
}

// MigrateAgentsFromJSON imports agents from a legacy JSON file into the DB.
func MigrateAgentsFromJSON(db *sql.DB, path string) (int, error) {
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
	var m map[string]agentregistry.Record
	if err := json.Unmarshal(data, &m); err != nil {
		return 0, err
	}
	n := 0
	for _, rec := range m {
		if strings.TrimSpace(rec.AgentID) == "" {
			continue
		}
		if err := UpsertAgent(db, rec); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
