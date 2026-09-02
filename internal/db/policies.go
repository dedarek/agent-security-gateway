package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

// PolicyRow is one row in policies table.
type PolicyRow struct {
	ID        int64           `json:"id"`
	AgentID   *string         `json:"agent_id"` // nil = global
	Axis      string          `json:"axis"`
	RuleID    string          `json:"rule_id"`
	Action    string          `json:"action"` // log | alert | block | confirm
	Enabled   bool            `json:"enabled"`
	Selector  json.RawMessage `json:"selector,omitempty"`
	UpdatedAt int64           `json:"updated_at"`
}

// UpsertPolicy inserts or updates a policy.
func UpsertPolicy(db *sql.DB, agentID *string, axis, ruleID, action string, enabled bool) error {
	return UpsertPolicyWithSelector(db, agentID, axis, ruleID, action, enabled, nil)
}

// UpsertPolicyWithSelector stores a structured match selector while retaining
// the legacy rule_id API. An empty selector means legacy matching.
func UpsertPolicyWithSelector(db *sql.DB, agentID *string, axis, ruleID, action string, enabled bool, selector json.RawMessage) error {
	now := time.Now().UnixMilli()
	if len(selector) == 0 {
		selector = json.RawMessage(`{}`)
	}
	_, err := db.Exec(`
INSERT INTO policies(agent_id, axis, rule_id, action, enabled, selector_json, updated_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(COALESCE(agent_id,''), rule_id) DO UPDATE SET
  axis=excluded.axis, action=excluded.action, enabled=excluded.enabled,
  selector_json=excluded.selector_json, updated_at=excluded.updated_at
`, agentID, axis, ruleID, action, boolToInt(enabled), string(selector), now)
	return err
}

// ListPolicies returns policies for an agent (including global if agentID != "").
// If agentID == "", returns global only. If agentID != "", returns agent-specific + global.
func ListPolicies(db *sql.DB, agentID string) ([]PolicyRow, error) {
	var rows *sql.Rows
	var err error
	if agentID == "" {
		rows, err = db.Query(`SELECT id, agent_id, axis, rule_id, action, enabled, selector_json, updated_at FROM policies WHERE agent_id IS NULL ORDER BY rule_id`)
	} else {
		rows, err = db.Query(`SELECT id, agent_id, axis, rule_id, action, enabled, selector_json, updated_at FROM policies WHERE agent_id=? OR agent_id IS NULL ORDER BY CASE WHEN agent_id IS NULL THEN 1 ELSE 0 END, rule_id`, agentID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRow
	for rows.Next() {
		var r PolicyRow
		var enabled int
		var sel string // modernc sqlite returns TEXT as string; scan into string then convert
		if err := rows.Scan(&r.ID, &r.AgentID, &r.Axis, &r.RuleID, &r.Action, &enabled, &sel, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Selector = json.RawMessage(sel)
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAllPolicies returns all policies (admin view).
func ListAllPolicies(db *sql.DB) ([]PolicyRow, error) {
	rows, err := db.Query(`SELECT id, agent_id, axis, rule_id, action, enabled, selector_json, updated_at FROM policies ORDER BY COALESCE(agent_id,''), rule_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRow
	for rows.Next() {
		var r PolicyRow
		var enabled int
		var sel string // modernc sqlite returns TEXT as string; scan into string then convert
		if err := rows.Scan(&r.ID, &r.AgentID, &r.Axis, &r.RuleID, &r.Action, &enabled, &sel, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Selector = json.RawMessage(sel)
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeletePolicy removes a policy by id.
func DeletePolicy(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM policies WHERE id=?`, id)
	return err
}

// GetPolicy returns one policy by agent+rule.
func GetPolicy(db *sql.DB, agentID *string, ruleID string) (*PolicyRow, error) {
	var r PolicyRow
	var enabled int
	var sel string
	var query string
	var args []any
	if agentID == nil {
		query = `SELECT id, agent_id, axis, rule_id, action, enabled, selector_json, updated_at FROM policies WHERE agent_id IS NULL AND rule_id=?`
		args = []any{ruleID}
	} else {
		query = `SELECT id, agent_id, axis, rule_id, action, enabled, selector_json, updated_at FROM policies WHERE agent_id=? AND rule_id=?`
		args = []any{*agentID, ruleID}
	}
	err := db.QueryRow(query, args...).Scan(&r.ID, &r.AgentID, &r.Axis, &r.RuleID, &r.Action, &enabled, &sel, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Selector = json.RawMessage(sel)
	r.Enabled = enabled != 0
	return &r, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
