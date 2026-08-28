// Package engine — per-agent policy engine.
// Reads policies from DB (via *sql.DB) and enforces per-agent overrides.
// Global policies (agent_id IS NULL) are fallback; agent-specific wins.
package engine

import (
	"context"
	"database/sql"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

type PolicyEngine struct {
	db       *sql.DB
	failMode api.FailMode
}

func NewPolicyEngine(db *sql.DB) *PolicyEngine {
	return &PolicyEngine{db: db, failMode: api.FailOpen}
}

func (e *PolicyEngine) Name() string           { return "policy.per_agent" }
func (e *PolicyEngine) Axis() api.Axis         { return api.AxisPermission }
func (e *PolicyEngine) FailMode() api.FailMode { return e.failMode }

func (e *PolicyEngine) EvaluatePre(_ context.Context, c *api.ToolCall) (*api.Signal, error) {
	if e.db == nil || c == nil {
		return nil, nil
	}
	// Lookup: try agent-specific first, then global
	agentID := strings.TrimSpace(c.Principal.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(c.Principal.SessionID)
	}
	// We check policies that match this tool or are wildcard.
	// For simplicity, rule_id is matched as tool name or "*".
	candidates := []string{c.ToolID, lastTool(c.ToolID), "*"}
	for _, ruleID := range candidates {
		// Try agent-specific
		if agentID != "" {
			if row, _ := getPolicy(e.db, &agentID, ruleID); row != nil && row.Enabled {
				return policyToSignal(row, ruleID), nil
			}
		}
		// Try global
		if row, _ := getPolicy(e.db, nil, ruleID); row != nil && row.Enabled {
			return policyToSignal(row, ruleID), nil
		}
	}
	return nil, nil
}

func (e *PolicyEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil
}

func (e *PolicyEngine) EvaluatePost(_ context.Context, _ *api.ToolCall, _ *api.ToolResult) (*api.Signal, error) {
	return nil, nil
}

func getPolicy(db *sql.DB, agentID *string, ruleID string) (*struct {
	Action string
	Enabled bool
}, error) {
	// Inline query to avoid import cycle with db package (we use raw SQL here)
	var action string
	var enabled int
	var query string
	var args []any
	if agentID == nil {
		query = `SELECT action, enabled FROM policies WHERE agent_id IS NULL AND rule_id=? AND enabled=1`
		args = []any{ruleID}
	} else {
		query = `SELECT action, enabled FROM policies WHERE agent_id=? AND rule_id=? AND enabled=1`
		args = []any{*agentID, ruleID}
	}
	err := db.QueryRow(query, args...).Scan(&action, &enabled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &struct {
		Action string
		Enabled bool
	}{Action: action, Enabled: enabled != 0}, nil
}

func policyToSignal(row *struct {
	Action string
	Enabled bool
}, ruleID string) *api.Signal {
	var verdict api.Verdict
	switch strings.ToLower(row.Action) {
	case "block":
		verdict = api.VerdictBlock
	case "confirm":
		verdict = api.VerdictConfirm
	case "redact":
		verdict = api.VerdictRedact
	case "alert":
		verdict = api.VerdictConfirm
	default: // log
		return &api.Signal{Axis: api.AxisPermission, Engine: "policy.per_agent", Verdict: api.VerdictAllow, Reasons: []string{"policy log: " + ruleID}}
	}
	return &api.Signal{
		Axis:    api.AxisPermission,
		Engine:  "policy.per_agent",
		Score:   80,
		Verdict: verdict,
		Reasons: []string{"per-agent policy: " + ruleID + " -> " + row.Action},
	}
}
